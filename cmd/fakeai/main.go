// Command fakeai is a deterministic stand-in for the Python AI service
// (atlas.ai.v1.AIService) for local development and frontend work. It produces
// plausible, schema-valid answers without any model:
//
//   - ProcessFeedback: one opinion per feedback (sentiment from the rating, a toy
//     embedding), clustered into the first candidate issue sharing a keyword, or a new one.
//   - ProcessConsultationTurn: asks two questions, then returns a solution citing the
//     evidence and the owner's answers, and proposes the last answer as a memory.
//
// Run it next to the API and set AI_GRPC_ADDR=localhost:50051:
//
//	go run ./cmd/fakeai            # listens on :50051 (FAKEAI_ADDR to change)
//
// FAKEAI_DELAY (e.g. "3s") slows every consultation turn down, to try loading states and
// concurrent requests.
package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"

	aiv1 "atlas-backend-core/gen/atlas/ai/v1"
)

const (
	embeddingDims = 16
	maxQuestions  = 2
	titleWords    = 5
)

func main() {
	addr := os.Getenv("FAKEAI_ADDR")
	if addr == "" {
		addr = ":50051"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	var delay time.Duration
	if v := os.Getenv("FAKEAI_DELAY"); v != "" {
		if delay, err = time.ParseDuration(v); err != nil {
			fmt.Fprintln(os.Stderr, "invalid FAKEAI_DELAY:", err)
			os.Exit(1)
		}
	}
	srv := grpc.NewServer()
	aiv1.RegisterAIServiceServer(srv, fakeAI{delay: delay})
	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()
	slog.Info("fake AI service listening", slog.String("addr", ln.Addr().String()))
	if err := srv.Serve(ln); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
}

type fakeAI struct {
	aiv1.UnimplementedAIServiceServer
	delay time.Duration
}

func (fakeAI) ProcessFeedback(_ context.Context, req *aiv1.ProcessFeedbackRequest) (*aiv1.ProcessFeedbackResponse, error) {
	fb := req.GetFeedback()
	text := strings.TrimSpace(fb.GetText())
	sentiment := aiv1.Sentiment_SENTIMENT_NEUTRAL
	switch r := fb.GetRating(); {
	case r <= 2.5:
		sentiment = aiv1.Sentiment_SENTIMENT_NEGATIVE
	case r >= 4:
		sentiment = aiv1.Sentiment_SENTIMENT_POSITIVE
	}
	severity := aiv1.Severity_SEVERITY_NOT_SEVERE
	if fb.GetRating() <= 1.5 {
		severity = aiv1.Severity_SEVERITY_SEVERE
	}
	title := strings.Join(firstWords(text, titleWords), " ")
	assignment := &aiv1.IssueAssignment{NewIssueTitle: title}
	for _, c := range req.GetCandidateIssues() {
		if sharesKeyword(c.GetTitle(), text) {
			assignment = &aiv1.IssueAssignment{ExistingIssueId: c.GetIssueId(), NewIssueTitle: c.GetTitle()}
			break
		}
	}
	return &aiv1.ProcessFeedbackResponse{Opinions: []*aiv1.ExtractedOpinion{{
		OpinionText:    title,
		EvidenceText:   text,
		Sentiment:      sentiment,
		Severity:       severity,
		Embedding:      embed(text),
		EmbeddingModel: "fakeai-hash-16",
		Issue:          assignment,
	}}}, nil
}

func (f fakeAI) ProcessConsultationTurn(ctx context.Context, req *aiv1.ProcessConsultationTurnRequest) (*aiv1.ProcessConsultationTurnResponse, error) {
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	resp := &aiv1.ProcessConsultationTurnResponse{ModelId: "fakeai", PromptVersion: "fake-v1"}
	var owners []*aiv1.ConversationMessage
	for _, m := range req.GetMessages() {
		if m.GetRole() == aiv1.MessageRole_MESSAGE_ROLE_OWNER {
			owners = append(owners, m)
		}
	}
	if last := lastOf(owners); last != nil {
		resp.MemoryCandidates = []*aiv1.MemoryCandidate{{
			FactText:              "Owner stated: " + last.GetContent(),
			SourceOwnerMessageIds: []string{last.GetId()},
		}}
	}

	ev := req.GetEvent()
	wantsSolution := ev == aiv1.ConsultationEvent_CONSULTATION_EVENT_GENERATE_NOW ||
		ev == aiv1.ConsultationEvent_CONSULTATION_EVENT_CONTINUE_DISCUSSION ||
		req.GetQuestionCount() >= maxQuestions
	if !wantsSolution {
		n := req.GetQuestionCount() + 1
		resp.Result = &aiv1.ProcessConsultationTurnResponse_NextQuestion{NextQuestion: &aiv1.NextQuestion{
			TopicKey: fmt.Sprintf("topic_%d", n),
			Text:     fmt.Sprintf("Question %d: what do you think causes %q?", n, req.GetContext().GetIssue().GetTitle()),
		}}
		return resp, nil
	}

	refs := &aiv1.References{}
	var evidence []*aiv1.CustomerEvidence
	for i, e := range req.GetContext().GetEvidence() {
		if i == 3 {
			break
		}
		refs.OpinionIds = append(refs.OpinionIds, e.GetOpinionId())
		evidence = append(evidence, &aiv1.CustomerEvidence{OpinionId: e.GetOpinionId(), Quote: e.GetEvidenceText(), Note: "Representative complaint"})
	}
	for _, o := range owners {
		refs.OwnerMessageIds = append(refs.OwnerMessageIds, o.GetId())
	}
	for _, m := range req.GetContext().GetMemories() {
		refs.MemoryIds = append(refs.MemoryIds, m.GetId())
	}
	resp.Result = &aiv1.ProcessConsultationTurnResponse_Solution{Solution: &aiv1.Solution{
		Findings:         []*aiv1.Finding{{Statement: fmt.Sprintf("%d customers raised this issue.", req.GetContext().GetStats().GetUniqueFeedbackCount()), References: refs}},
		CustomerEvidence: evidence,
		Hypotheses:       []*aiv1.Hypothesis{{Statement: "The process behind this issue is not standardized.", Confidence: "medium", References: refs}},
		Recommendations:  []*aiv1.Recommendation{{Title: "Write a checklist", Description: "Define and train a simple checklist for the team.", Priority: "high", References: refs}},
		SuccessIndicators: []*aiv1.SuccessIndicator{{
			Metric: "Negative opinions on this issue", Target: "-50%", Timeframe: "4 weeks",
		}},
	}}
	return resp, nil
}

func firstWords(s string, n int) []string {
	words := strings.Fields(s)
	if len(words) > n {
		words = words[:n]
	}
	if len(words) == 0 {
		return []string{"General feedback"}
	}
	return words
}

func sharesKeyword(title, text string) bool {
	text = strings.ToLower(text)
	for _, w := range strings.Fields(strings.ToLower(title)) {
		if len(w) > 3 && strings.Contains(text, w) {
			return true
		}
	}
	return false
}

func embed(text string) []float32 {
	v := make([]float32, embeddingDims)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[h.Sum32()%embeddingDims]++
	}
	return v
}

func lastOf[T any](s []T) T {
	var zero T
	if len(s) == 0 {
		return zero
	}
	return s[len(s)-1]
}
