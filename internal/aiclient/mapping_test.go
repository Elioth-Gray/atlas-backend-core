package aiclient

import (
	"testing"

	"github.com/google/uuid"

	aiv1 "atlas-backend-core/gen/atlas/ai/v1"
	"atlas-backend-core/internal/domain"
)

func TestFromTurnResponse(t *testing.T) {
	op := uuid.NewString()
	resp := &aiv1.ProcessConsultationTurnResponse{
		Result: &aiv1.ProcessConsultationTurnResponse_Solution{Solution: &aiv1.Solution{
			Findings:         []*aiv1.Finding{{Statement: "s", References: &aiv1.References{OpinionIds: []string{op, "not-a-uuid"}}}},
			CustomerEvidence: []*aiv1.CustomerEvidence{{OpinionId: op, Quote: "q"}},
		}},
		MemoryCandidates: []*aiv1.MemoryCandidate{{FactText: "f", SourceOwnerMessageIds: []string{"bogus"}}},
		ModelId:          "gemini",
	}
	got := fromTurnResponse(resp)
	if got.Question != nil || got.Solution == nil {
		t.Fatalf("want a solution, got %+v", got)
	}
	ids := got.Solution.Findings[0].References.OpinionIDs
	if len(ids) != 2 || ids[0].String() != op || ids[1] != uuid.Nil {
		t.Errorf("unparsable IDs must map to uuid.Nil so validation rejects them: %v", ids)
	}
	if got.MemoryCandidates[0].SourceOwnerMessageIDs[0] != uuid.Nil || got.ModelID != "gemini" {
		t.Errorf("candidates = %+v", got.MemoryCandidates)
	}

	q := fromTurnResponse(&aiv1.ProcessConsultationTurnResponse{
		Result: &aiv1.ProcessConsultationTurnResponse_NextQuestion{NextQuestion: &aiv1.NextQuestion{TopicKey: "k", Text: "t"}},
	})
	if q.Question == nil || q.Solution != nil || q.Question.TopicKey != "k" {
		t.Errorf("want a question, got %+v", q)
	}
}

func TestFromFeedbackResponse(t *testing.T) {
	issue := uuid.NewString()
	got := fromFeedbackResponse(&aiv1.ProcessFeedbackResponse{Opinions: []*aiv1.ExtractedOpinion{
		{Sentiment: aiv1.Sentiment_SENTIMENT_NEGATIVE, Severity: aiv1.Severity_SEVERITY_SEVERE, Issue: &aiv1.IssueAssignment{ExistingIssueId: issue}},
		{Sentiment: aiv1.Sentiment_SENTIMENT_UNSPECIFIED, Severity: aiv1.Severity_SEVERITY_NOT_SEVERE, Issue: &aiv1.IssueAssignment{ExistingIssueId: "x", NewIssueTitle: "New"}},
	}})
	a, b := got.Opinions[0], got.Opinions[1]
	if a.Sentiment != domain.SentimentNegative || !a.Severe || a.ExistingIssueID == nil || a.ExistingIssueID.String() != issue {
		t.Errorf("first opinion = %+v", a)
	}
	if b.Sentiment != "" || b.Severe || b.ExistingIssueID != nil || b.IssueTitle != "New" {
		t.Errorf("second opinion = %+v", b)
	}
}

func TestEnumMappingsCoverEveryValue(t *testing.T) {
	for _, e := range []domain.TurnEvent{domain.EventStart, domain.EventOwnerReply, domain.EventSkipQuestion, domain.EventGenerateNow, domain.EventContinueDiscussion} {
		if toEvent(e) == aiv1.ConsultationEvent_CONSULTATION_EVENT_UNSPECIFIED {
			t.Errorf("event %s is not mapped", e)
		}
	}
	for _, s := range []domain.Sentiment{domain.SentimentNegative, domain.SentimentPositive, domain.SentimentNeutral} {
		if fromSentiment(toSentiment(s)) != s {
			t.Errorf("sentiment %s does not round-trip", s)
		}
	}
}
