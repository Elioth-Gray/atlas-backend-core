package service

import (
	"errors"
	"fmt"
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
		if len(q.TopicKey) > maxTopicKeyLength {
			return res, fmt.Errorf("topic_key is longer than %d characters", maxTopicKeyLength)
		}
		res.Question = &q
	case res.Solution != nil:
		if err := validateSolution(*res.Solution, refs); err != nil {
			return res, err
		}
	default:
		return res, errors.New("response contains neither a question nor a solution")
	}
	res.MemoryCandidates = validCandidates(res.MemoryCandidates, refs.owners)
	res.ModelID = truncateRunes(strings.TrimSpace(res.ModelID), maxModelIDLength)
	res.PromptVersion = truncateRunes(strings.TrimSpace(res.PromptVersion), maxPromptVersionLength)
	return res, nil
}

func validateSolution(sol domain.SolutionData, refs turnRefs) error {
	switch {
	case len(sol.Findings) == 0:
		return errors.New("solution.findings must not be empty")
	case len(sol.CustomerEvidence) == 0:
		return errors.New("solution.customer_evidence must not be empty")
	case len(sol.Hypotheses) == 0:
		return errors.New("solution.hypotheses must not be empty")
	case len(sol.Recommendations) == 0:
		return errors.New("solution.recommendations must not be empty")
	case len(sol.SuccessIndicators) == 0:
		return errors.New("solution.success_indicators must not be empty")
	}
	for i, f := range sol.Findings {
		path := fmt.Sprintf("solution.findings[%d]", i)
		if strings.TrimSpace(f.Statement) == "" {
			return errors.New(path + ".statement is empty")
		}
		if err := refs.check(path, f.References); err != nil {
			return err
		}
	}
	for i, e := range sol.CustomerEvidence {
		path := fmt.Sprintf("solution.customer_evidence[%d]", i)
		if !refs.opinions[e.OpinionID] {
			return fmt.Errorf("%s cites unknown opinion %s", path, e.OpinionID)
		}
		if strings.TrimSpace(e.Quote) == "" {
			return errors.New(path + ".quote is empty")
		}
	}
	for i, h := range sol.Hypotheses {
		path := fmt.Sprintf("solution.hypotheses[%d]", i)
		if strings.TrimSpace(h.Statement) == "" {
			return errors.New(path + ".statement is empty")
		}
		if err := refs.check(path, h.References); err != nil {
			return err
		}
	}
	for i, r := range sol.Recommendations {
		path := fmt.Sprintf("solution.recommendations[%d]", i)
		if strings.TrimSpace(r.Title) == "" {
			return errors.New(path + ".title is empty")
		}
		if err := refs.check(path, r.References); err != nil {
			return err
		}
	}
	for i, ind := range sol.SuccessIndicators {
		if strings.TrimSpace(ind.Metric) == "" {
			return fmt.Errorf("solution.success_indicators[%d].metric is empty", i)
		}
	}
	return nil
}

// check verifies every ID in refs.
func (t turnRefs) check(path string, refs domain.References) error {
	for _, id := range refs.OpinionIDs {
		if !t.opinions[id] {
			return fmt.Errorf("%s cites unknown opinion %s", path, id)
		}
	}
	for _, id := range refs.OwnerMessageIDs {
		if !t.owners[id] {
			return fmt.Errorf("%s cites unknown owner message %s", path, id)
		}
	}
	for _, id := range refs.MemoryIDs {
		if !t.memories[id] {
			return fmt.Errorf("%s cites unknown memory %s", path, id)
		}
	}
	return nil
}

// validCandidates keeps the memory candidates with a fact and at least one source, all of
// them OWNER messages of the session.
func validCandidates(in []domain.MemoryCandidate, owners map[uuid.UUID]bool) []domain.MemoryCandidate {
	out := make([]domain.MemoryCandidate, 0, len(in))
	for _, c := range in {
		c.FactText = strings.TrimSpace(c.FactText)
		ok := c.FactText != "" && len(c.SourceOwnerMessageIDs) > 0
		for _, id := range c.SourceOwnerMessageIDs {
			ok = ok && owners[id]
		}
		if ok {
			out = append(out, c)
			continue
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
