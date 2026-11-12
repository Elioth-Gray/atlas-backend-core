package service

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"atlas-backend-core/internal/domain"
)

// Column limits of consultation_messages.
const (
	maxTopicKeyLength      = 100
	maxModelIDLength       = 100
	maxPromptVersionLength = 50
)

// turnRefs are the IDs an AI response may cite: evidence opinions and active memories from
// the session snapshot, and OWNER messages of the session. Everything else is either
// hallucinated or belongs to another tenant.
type turnRefs struct {
	opinions map[uuid.UUID]bool
	owners   map[uuid.UUID]bool
	memories map[uuid.UUID]bool
}

func newTurnRefs(snapshot domain.ContextSnapshot, msgs []domain.ConsultationMessage) turnRefs {
	refs := turnRefs{
		opinions: make(map[uuid.UUID]bool, len(snapshot.Evidence)),
		owners:   ownerMessageIDs(msgs),
		memories: make(map[uuid.UUID]bool, len(snapshot.Memories)),
	}
	for _, e := range snapshot.Evidence {
		refs.opinions[e.OpinionID] = true
	}
	for _, m := range snapshot.Memories {
		refs.memories[m.ID] = true
	}
	return refs
}

// validateTurnResult checks the AI response before anything is stored (claim-level
// grounding and schema check). A question needs a topic and a text; a solution needs all
// five components and may only cite known IDs. Memory candidates citing anything but
// OWNER messages of the session are dropped rather than failing the turn. The returned
// result is normalized (trimmed, column limits applied).
func validateTurnResult(res ConsultationTurnResult, refs turnRefs) (ConsultationTurnResult, error) {
	switch {
	case res.Question != nil && res.Solution != nil:
		return res, errors.New("response contains both a question and a solution")
	case res.Question != nil:
		q := NextQuestion{TopicKey: strings.TrimSpace(res.Question.TopicKey), Text: strings.TrimSpace(res.Question.Text)}
		if q.TopicKey == "" || q.Text == "" {
			return res, errors.New("next_question needs a topic_key and a text")
		}
		if len([]rune(q.TopicKey)) > maxTopicKeyLength {
			return res, fmt.Errorf("topic_key is longer than %d characters", maxTopicKeyLength)
		}
		res.Question = &q
	case res.Solution != nil:
		sol, err := validateSolution(*res.Solution, refs)
		if err != nil {
			return res, err
		}
		res.Solution = &sol
	default:
		return res, errors.New("response contains neither a question nor a solution")
	}
	res.MemoryCandidates = validCandidates(res.MemoryCandidates, refs.owners)
	res.ModelID = truncateRunes(strings.TrimSpace(res.ModelID), maxModelIDLength)
	res.PromptVersion = truncateRunes(strings.TrimSpace(res.PromptVersion), maxPromptVersionLength)
	return res, nil
}

func validateSolution(sol domain.SolutionData, refs turnRefs) (domain.SolutionData, error) {
	switch {
	case len(sol.Findings) == 0:
		return sol, errors.New("solution.findings must not be empty")
	case len(sol.CustomerEvidence) == 0:
		return sol, errors.New("solution.customer_evidence must not be empty")
	case len(sol.Hypotheses) == 0:
		return sol, errors.New("solution.hypotheses must not be empty")
	case len(sol.Recommendations) == 0:
		return sol, errors.New("solution.recommendations must not be empty")
	case len(sol.SuccessIndicators) == 0:
		return sol, errors.New("solution.success_indicators must not be empty")
	}
	findings := make([]domain.SolutionFinding, len(sol.Findings))
	for i, f := range sol.Findings {
		path := fmt.Sprintf("solution.findings[%d]", i)
		f.Statement = strings.TrimSpace(f.Statement)
		if f.Statement == "" {
			return sol, errors.New(path + ".statement is empty")
		}
		normRefs, err := refs.check(path, f.References)
		if err != nil {
			return sol, err
		}
		f.References = normRefs
		findings[i] = f
	}
	evidence := make([]domain.SolutionEvidence, len(sol.CustomerEvidence))
	for i, e := range sol.CustomerEvidence {
		path := fmt.Sprintf("solution.customer_evidence[%d]", i)
		if !refs.opinions[e.OpinionID] {
			return sol, fmt.Errorf("%s cites unknown opinion %s", path, e.OpinionID)
		}
		e.Quote = strings.TrimSpace(e.Quote)
		e.Note = strings.TrimSpace(e.Note)
		if e.Quote == "" {
			return sol, errors.New(path + ".quote is empty")
		}
		evidence[i] = e
	}
	hypotheses := make([]domain.SolutionHypothesis, len(sol.Hypotheses))
	for i, h := range sol.Hypotheses {
		path := fmt.Sprintf("solution.hypotheses[%d]", i)
		h.Statement = strings.TrimSpace(h.Statement)
		h.Confidence = strings.TrimSpace(h.Confidence)
		if h.Statement == "" {
			return sol, errors.New(path + ".statement is empty")
		}
		normRefs, err := refs.check(path, h.References)
		if err != nil {
			return sol, err
		}
		h.References = normRefs
		hypotheses[i] = h
	}
	recommendations := make([]domain.SolutionRecommendation, len(sol.Recommendations))
	for i, r := range sol.Recommendations {
		path := fmt.Sprintf("solution.recommendations[%d]", i)
		r.Title = strings.TrimSpace(r.Title)
		r.Description = strings.TrimSpace(r.Description)
		r.Priority = strings.TrimSpace(r.Priority)
		if r.Title == "" {
			return sol, errors.New(path + ".title is empty")
		}
		normRefs, err := refs.check(path, r.References)
		if err != nil {
			return sol, err
		}
		r.References = normRefs
		recommendations[i] = r
	}
	indicators := make([]domain.SolutionIndicator, len(sol.SuccessIndicators))
	for i, ind := range sol.SuccessIndicators {
		ind.Metric = strings.TrimSpace(ind.Metric)
		ind.Target = strings.TrimSpace(ind.Target)
		ind.Timeframe = strings.TrimSpace(ind.Timeframe)
		if ind.Metric == "" {
			return sol, fmt.Errorf("solution.success_indicators[%d].metric is empty", i)
		}
		indicators[i] = ind
	}
	return domain.SolutionData{
		Findings:          findings,
		CustomerEvidence:  evidence,
		Hypotheses:        hypotheses,
		Recommendations:   recommendations,
		SuccessIndicators: indicators,
	}, nil
}

// check verifies every ID in refs, requires at least one cited source, and deduplicates IDs.
func (t turnRefs) check(path string, refs domain.References) (domain.References, error) {
	opinions, err := checkAndDedupe(path, "opinion", refs.OpinionIDs, t.opinions)
	if err != nil {
		return domain.References{}, err
	}
	owners, err := checkAndDedupe(path, "owner message", refs.OwnerMessageIDs, t.owners)
	if err != nil {
		return domain.References{}, err
	}
	memories, err := checkAndDedupe(path, "memory", refs.MemoryIDs, t.memories)
	if err != nil {
		return domain.References{}, err
	}
	if len(opinions)+len(owners)+len(memories) == 0 {
		return domain.References{}, fmt.Errorf("%s.references must cite at least one source", path)
	}
	return domain.References{
		OpinionIDs:      opinions,
		OwnerMessageIDs: owners,
		MemoryIDs:       memories,
	}, nil
}

func checkAndDedupe(path, kind string, ids []uuid.UUID, allowed map[uuid.UUID]bool) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !allowed[id] {
			return nil, fmt.Errorf("%s cites unknown %s %s", path, kind, id)
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// validCandidates keeps the memory candidates with a fact and at least one source, all of
// them OWNER messages of the session, truncating FactText and deduplicating sources.
func validCandidates(in []domain.MemoryCandidate, owners map[uuid.UUID]bool) []domain.MemoryCandidate {
	out := make([]domain.MemoryCandidate, 0, min(len(in), MaxConfirmedMemories))
	for _, c := range in {
		c.FactText = truncateRunes(strings.TrimSpace(c.FactText), MaxMemoryFactLength)
		if c.FactText == "" || len(c.SourceOwnerMessageIDs) == 0 {
			continue
		}
		sources := make([]uuid.UUID, 0, len(c.SourceOwnerMessageIDs))
		ok := true
		for _, id := range c.SourceOwnerMessageIDs {
			if !owners[id] {
				ok = false
				break
			}
			if !slices.Contains(sources, id) {
				sources = append(sources, id)
			}
		}
		if ok {
			c.SourceOwnerMessageIDs = sources
			out = append(out, c)
			if len(out) >= MaxConfirmedMemories {
				break
			}
		}
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
