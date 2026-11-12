package service

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"atlas-backend-core/internal/domain"
)

var (
	opinionID = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	ownerID   = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	memoryID  = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	aiMsgID   = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	foreignID = uuid.MustParse("99999999-9999-4999-8999-999999999999")
)

func testRefs() turnRefs {
	snapshot := domain.ContextSnapshot{
		Evidence: []domain.EvidenceOpinion{{OpinionID: opinionID}},
		Memories: []domain.MemoryFact{{ID: memoryID}},
	}
	msgs := []domain.ConsultationMessage{
		{ID: aiMsgID, Role: domain.RoleAtlasAI},
		{ID: ownerID, Role: domain.RoleOwner},
	}
	return newTurnRefs(snapshot, msgs)
}

func validSolution() *domain.SolutionData {
	refs := domain.References{
		OpinionIDs: []uuid.UUID{opinionID}, OwnerMessageIDs: []uuid.UUID{ownerID}, MemoryIDs: []uuid.UUID{memoryID},
	}
	return &domain.SolutionData{
		Findings:          []domain.SolutionFinding{{Statement: "Slow service", References: refs}},
		CustomerEvidence:  []domain.SolutionEvidence{{OpinionID: opinionID, Quote: "waited 40 minutes"}},
		Hypotheses:        []domain.SolutionHypothesis{{Statement: "Understaffed", References: refs}},
		Recommendations:   []domain.SolutionRecommendation{{Title: "Add a barista", References: refs}},
		SuccessIndicators: []domain.SolutionIndicator{{Metric: "Wait time"}},
	}
}

func TestValidateTurnResult(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(r *ConsultationTurnResult)
		wantErr string
	}{
		{name: "valid question", mutate: func(r *ConsultationTurnResult) {
			r.Question, r.Solution = &NextQuestion{TopicKey: " staffing ", Text: "How many staff?"}, nil
		}},
		{name: "valid solution citing snapshot and owner ids"},
		{name: "question without topic", wantErr: "topic_key", mutate: func(r *ConsultationTurnResult) {
			r.Question, r.Solution = &NextQuestion{Text: "?"}, nil
		}},
		{name: "topic too long", wantErr: "topic_key", mutate: func(r *ConsultationTurnResult) {
			r.Question, r.Solution = &NextQuestion{TopicKey: strings.Repeat("x", 101), Text: "?"}, nil
		}},
		{name: "both question and solution", wantErr: "both", mutate: func(r *ConsultationTurnResult) {
			r.Question = &NextQuestion{TopicKey: "a", Text: "b"}
		}},
		{name: "neither", wantErr: "neither", mutate: func(r *ConsultationTurnResult) { r.Solution = nil }},
		{name: "missing component", wantErr: "hypotheses", mutate: func(r *ConsultationTurnResult) { r.Solution.Hypotheses = nil }},
		{name: "hallucinated opinion", wantErr: "unknown opinion", mutate: func(r *ConsultationTurnResult) {
			r.Solution.Findings[0].References.OpinionIDs = []uuid.UUID{foreignID}
		}},
		{name: "unparsable id mapped to nil", wantErr: "unknown opinion", mutate: func(r *ConsultationTurnResult) {
			r.Solution.CustomerEvidence[0].OpinionID = uuid.Nil
		}},
		{name: "ai message cited as owner message", wantErr: "unknown owner message", mutate: func(r *ConsultationTurnResult) {
			r.Solution.Recommendations[0].References.OwnerMessageIDs = []uuid.UUID{aiMsgID}
		}},
		{name: "memory of another shop", wantErr: "unknown memory", mutate: func(r *ConsultationTurnResult) {
			r.Solution.Hypotheses[0].References.MemoryIDs = []uuid.UUID{foreignID}
		}},
		{name: "blank recommendation title", wantErr: "title", mutate: func(r *ConsultationTurnResult) {
			r.Solution.Recommendations[0].Title = "  "
		}},
		{name: "claim without references", wantErr: "cite at least one source", mutate: func(r *ConsultationTurnResult) {
			r.Solution.Findings[0].References = domain.References{}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := ConsultationTurnResult{Solution: validSolution()}
			if tt.mutate != nil {
				tt.mutate(&res)
			}
			got, err := validateTurnResult(res, testRefs())
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.Question != nil && got.Question.TopicKey != "staffing" {
					t.Errorf("topic not trimmed: %q", got.Question.TopicKey)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateTurnResult_FiltersMemoryCandidates(t *testing.T) {
	res := ConsultationTurnResult{
		Question: &NextQuestion{TopicKey: "a", Text: "b"},
		MemoryCandidates: []domain.MemoryCandidate{
			{FactText: " Two baristas on weekends ", SourceOwnerMessageIDs: []uuid.UUID{ownerID, ownerID}},
			{FactText: "cites the AI", SourceOwnerMessageIDs: []uuid.UUID{aiMsgID}},
			{FactText: "no sources"},
			{FactText: " ", SourceOwnerMessageIDs: []uuid.UUID{ownerID}},
			{FactText: "mixed", SourceOwnerMessageIDs: []uuid.UUID{ownerID, foreignID}},
		},
		ModelID: strings.Repeat("m", 150),
	}
	got, err := validateTurnResult(res, testRefs())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.MemoryCandidates) != 1 || got.MemoryCandidates[0].FactText != "Two baristas on weekends" || len(got.MemoryCandidates[0].SourceOwnerMessageIDs) != 1 {
		t.Errorf("candidates = %+v, want only the valid deduplicated one", got.MemoryCandidates)
	}
	if len(got.ModelID) != maxModelIDLength {
		t.Errorf("model id not truncated: %d", len(got.ModelID))
	}
}

func TestEventAllowed(t *testing.T) {
	tests := []struct {
		status domain.SessionStatus
		event  domain.TurnEvent
		want   bool
	}{
		{domain.SessionCollectingContext, domain.EventOwnerReply, true},
		{domain.SessionCollectingContext, domain.EventSkipQuestion, true},
		{domain.SessionCollectingContext, domain.EventGenerateNow, true},
		{domain.SessionCollectingContext, domain.EventContinueDiscussion, false},
		{domain.SessionSolutionReady, domain.EventContinueDiscussion, true},
		{domain.SessionSolutionReady, domain.EventGenerateNow, true},
		{domain.SessionSolutionReady, domain.EventOwnerReply, false},
		{domain.SessionSolutionReady, domain.EventSkipQuestion, false},
		{domain.SessionArchived, domain.EventGenerateNow, false},
		{domain.SessionCollectingContext, domain.EventStart, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status)+"/"+string(tt.event), func(t *testing.T) {
			if got := eventAllowed(tt.status, tt.event); got != tt.want {
				t.Errorf("eventAllowed = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestNewMemory(t *testing.T) {
	owners := map[uuid.UUID]bool{ownerID: true}
	tests := []struct {
		name    string
		in      MemoryInput
		wantErr bool
	}{
		{name: "valid, duplicates removed", in: MemoryInput{FactText: " fact ", SourceOwnerMessageIDs: []uuid.UUID{ownerID, ownerID}}},
		{name: "blank fact", in: MemoryInput{FactText: " ", SourceOwnerMessageIDs: []uuid.UUID{ownerID}}, wantErr: true},
		{name: "no sources", in: MemoryInput{FactText: "fact"}, wantErr: true},
		{name: "source is not an owner message", in: MemoryInput{FactText: "fact", SourceOwnerMessageIDs: []uuid.UUID{aiMsgID}}, wantErr: true},
		{name: "too long", in: MemoryInput{FactText: strings.Repeat("x", MaxMemoryFactLength+1), SourceOwnerMessageIDs: []uuid.UUID{ownerID}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mem, err := newMemory(0, tt.in, owners)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %t", err, tt.wantErr)
			}
			if !tt.wantErr && (mem.FactText != "fact" || len(mem.SourceOwnerMessageIDs.V) != 1) {
				t.Errorf("memory = %+v", mem)
			}
		})
	}
}

func TestLastQuestionTopicAndLatestSolution(t *testing.T) {
	topicA, topicB := "a", "b"
	sol := domain.SolutionData{Findings: []domain.SolutionFinding{{Statement: "x"}}}
	msgs := []domain.ConsultationMessage{
		{Role: domain.RoleAtlasAI, MessageType: domain.MessageText, TopicKey: &topicA},
		{Role: domain.RoleAtlasAI, MessageType: domain.MessageSolution, SolutionData: domain.NullJSONB[domain.SolutionData]{V: sol, Valid: true}},
		{Role: domain.RoleAtlasAI, MessageType: domain.MessageText, TopicKey: &topicB},
		{Role: domain.RoleOwner, MessageType: domain.MessageText},
	}
	if got := lastQuestionTopic(msgs); got != "b" {
		t.Errorf("lastQuestionTopic = %q, want b", got)
	}
	if got := latestSolution(msgs); got == nil || got.Findings[0].Statement != "x" {
		t.Errorf("latestSolution = %+v", got)
	}
	if latestSolution(nil) != nil || lastQuestionTopic(nil) != "" {
		t.Error("empty history must yield nothing")
	}
}
