package aiclient

import (
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	aiv1 "atlas-backend-core/gen/atlas/ai/v1"
	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/service"
)

// --- ProcessFeedback ---------------------------------------------------------------

func toFeedbackRequest(req service.FeedbackAnalysisRequest) *aiv1.ProcessFeedbackRequest {
	fb := req.Feedback
	out := &aiv1.ProcessFeedbackRequest{
		ShopId: fb.ShopID.String(),
		Feedback: &aiv1.Feedback{
			Id:           fb.ID.String(),
			CustomerName: deref(fb.Name),
			Rating:       fb.Rating,
			Text:         fb.Text,
			CreateTime:   timestamppb.New(fb.CreatedAt),
		},
		Shop:             toShop(req.Shop),
		CandidateIssues:  make([]*aiv1.CandidateIssue, 0, len(req.CandidateIssues)),
		CandidateVectors: make([]*aiv1.CandidateVector, 0, len(req.CandidateVectors)),
	}
	for _, c := range req.CandidateIssues {
		out.CandidateIssues = append(out.CandidateIssues, &aiv1.CandidateIssue{
			IssueId: c.ID.String(), Title: c.Title, OpinionCount: c.OpinionCount,
		})
	}
	for _, v := range req.CandidateVectors {
		out.CandidateVectors = append(out.CandidateVectors, &aiv1.CandidateVector{
			OpinionId:      v.OpinionID.String(),
			IssueId:        uuidString(v.IssueID),
			Embedding:      v.Embedding.Slice(),
			EmbeddingModel: v.EmbeddingModel,
		})
	}
	return out
}

func fromFeedbackResponse(resp *aiv1.ProcessFeedbackResponse) service.FeedbackAnalysis {
	out := service.FeedbackAnalysis{Opinions: make([]service.ExtractedOpinion, 0, len(resp.GetOpinions()))}
	for _, op := range resp.GetOpinions() {
		out.Opinions = append(out.Opinions, service.ExtractedOpinion{
			OpinionText:     op.GetOpinionText(),
			EvidenceText:    op.GetEvidenceText(),
			Sentiment:       fromSentiment(op.GetSentiment()),
			Severe:          op.GetSeverity() == aiv1.Severity_SEVERITY_SEVERE,
			Embedding:       op.GetEmbedding(),
			EmbeddingModel:  op.GetEmbeddingModel(),
			ExistingIssueID: parseOptionalUUID(op.GetIssue().GetExistingIssueId()),
			IssueTitle:      op.GetIssue().GetNewIssueTitle(),
		})
	}
	return out
}

// --- ProcessConsultationTurn -----------------------------------------------------------

func toTurnRequest(req service.ConsultationTurnRequest) *aiv1.ProcessConsultationTurnRequest {
	out := &aiv1.ProcessConsultationTurnRequest{
		SessionId:     req.SessionID.String(),
		RequestId:     req.RequestID.String(),
		Event:         toEvent(req.Event),
		Context:       toSnapshot(req.Snapshot),
		Messages:      make([]*aiv1.ConversationMessage, 0, len(req.Messages)),
		AskedTopics:   req.AskedTopics,
		SkippedTopics: req.SkippedTopics,
		QuestionCount: int32(min(req.QuestionCount, 1<<15)), //nolint:gosec // bounded above
	}
	if req.PreviousSolution != nil {
		out.PreviousSolution = toSolution(*req.PreviousSolution)
	}
	for _, m := range req.Messages {
		msg := &aiv1.ConversationMessage{
			Id:         m.ID.String(),
			Role:       toRole(m.Role),
			Content:    deref(m.Content),
			TopicKey:   deref(m.TopicKey),
			CreateTime: timestamppb.New(m.CreatedAt),
		}
		if m.SolutionData.Valid {
			msg.Solution = toSolution(m.SolutionData.V)
		}
		out.Messages = append(out.Messages, msg)
	}
	return out
}

func fromTurnResponse(resp *aiv1.ProcessConsultationTurnResponse) service.ConsultationTurnResult {
	out := service.ConsultationTurnResult{
		ModelID:          resp.GetModelId(),
		PromptVersion:    resp.GetPromptVersion(),
		MemoryCandidates: make([]domain.MemoryCandidate, 0, len(resp.GetMemoryCandidates())),
	}
	if q := resp.GetNextQuestion(); q != nil {
		out.Question = &service.NextQuestion{TopicKey: q.GetTopicKey(), Text: q.GetText()}
	}
	if s := resp.GetSolution(); s != nil {
		sol := fromSolution(s)
		out.Solution = &sol
	}
	for _, c := range resp.GetMemoryCandidates() {
		out.MemoryCandidates = append(out.MemoryCandidates, domain.MemoryCandidate{
			FactText:              c.GetFactText(),
			SourceOwnerMessageIDs: parseUUIDs(c.GetSourceOwnerMessageIds()),
		})
	}
	return out
}

func toSnapshot(s domain.ContextSnapshot) *aiv1.ContextSnapshot {
	out := &aiv1.ContextSnapshot{
		Issue:  &aiv1.IssueSummary{Id: s.Issue.ID.String(), Title: s.Issue.Title},
		Period: &aiv1.Period{StartTime: optionalTimestamp(s.Period.From), EndTime: optionalTimestamp(s.Period.To)},
		Stats: &aiv1.IssueStats{
			UniqueFeedbackCount: s.Stats.UniqueFeedbackCount,
			OpinionCount:        s.Stats.OpinionCount,
			AverageRating:       s.Stats.AverageRating,
			NegativeCount:       s.Stats.Sentiment.Negative,
			PositiveCount:       s.Stats.Sentiment.Positive,
			NeutralCount:        s.Stats.Sentiment.Neutral,
			SevereCount:         s.Stats.Severity.Severe,
			NotSevereCount:      s.Stats.Severity.NotSevere,
		},
		Evidence: make([]*aiv1.EvidenceOpinion, 0, len(s.Evidence)),
		Shop:     toShop(s.Shop),
		Memories: make([]*aiv1.BusinessMemory, 0, len(s.Memories)),
	}
	for _, e := range s.Evidence {
		out.Evidence = append(out.Evidence, &aiv1.EvidenceOpinion{
			OpinionId:    e.OpinionID.String(),
			OpinionText:  e.OpinionText,
			EvidenceText: deref(e.EvidenceText),
			Sentiment:    toSentiment(e.Sentiment),
			Severity:     toSeverity(e.Severity),
			FullFeedback: e.FullFeedback,
			Rating:       e.Rating,
			CreateTime:   timestamppb.New(e.CreatedAt),
		})
	}
	for _, m := range s.Memories {
		out.Memories = append(out.Memories, &aiv1.BusinessMemory{Id: m.ID.String(), FactText: m.FactText})
	}
	if p := s.PriorIssueSolution; p != nil {
		out.PriorIssueSolution = &aiv1.PriorSolution{
			SessionId:   p.SessionID.String(),
			ArchiveTime: timestamppb.New(p.ArchivedAt),
			Solution:    toSolution(p.Solution),
		}
	}
	return out
}

func toShop(p domain.ShopProfile) *aiv1.ShopProfile {
	return &aiv1.ShopProfile{
		Id: p.ID.String(), Name: p.Name, Description: p.Description, Category: p.Category, Scale: p.Scale,
	}
}

func toSolution(s domain.SolutionData) *aiv1.Solution {
	out := &aiv1.Solution{}
	for _, f := range s.Findings {
		out.Findings = append(out.Findings, &aiv1.Finding{Statement: f.Statement, References: toRefs(f.References)})
	}
	for _, e := range s.CustomerEvidence {
		out.CustomerEvidence = append(out.CustomerEvidence, &aiv1.CustomerEvidence{
			OpinionId: e.OpinionID.String(), Quote: e.Quote, Note: e.Note,
		})
	}
	for _, h := range s.Hypotheses {
		out.Hypotheses = append(out.Hypotheses, &aiv1.Hypothesis{
			Statement: h.Statement, Confidence: h.Confidence, References: toRefs(h.References),
		})
	}
	for _, r := range s.Recommendations {
		out.Recommendations = append(out.Recommendations, &aiv1.Recommendation{
			Title: r.Title, Description: r.Description, Priority: r.Priority, References: toRefs(r.References),
		})
	}
	for _, i := range s.SuccessIndicators {
		out.SuccessIndicators = append(out.SuccessIndicators, &aiv1.SuccessIndicator{
			Metric: i.Metric, Target: i.Target, Timeframe: i.Timeframe,
		})
	}
	return out
}

func fromSolution(s *aiv1.Solution) domain.SolutionData {
	out := domain.SolutionData{
		Findings:          make([]domain.SolutionFinding, 0, len(s.GetFindings())),
		CustomerEvidence:  make([]domain.SolutionEvidence, 0, len(s.GetCustomerEvidence())),
		Hypotheses:        make([]domain.SolutionHypothesis, 0, len(s.GetHypotheses())),
		Recommendations:   make([]domain.SolutionRecommendation, 0, len(s.GetRecommendations())),
		SuccessIndicators: make([]domain.SolutionIndicator, 0, len(s.GetSuccessIndicators())),
	}
	for _, f := range s.GetFindings() {
		out.Findings = append(out.Findings, domain.SolutionFinding{Statement: f.GetStatement(), References: fromRefs(f.GetReferences())})
	}
	for _, e := range s.GetCustomerEvidence() {
		out.CustomerEvidence = append(out.CustomerEvidence, domain.SolutionEvidence{
			OpinionID: parseUUID(e.GetOpinionId()), Quote: e.GetQuote(), Note: e.GetNote(),
		})
	}
	for _, h := range s.GetHypotheses() {
		out.Hypotheses = append(out.Hypotheses, domain.SolutionHypothesis{
			Statement: h.GetStatement(), Confidence: h.GetConfidence(), References: fromRefs(h.GetReferences()),
		})
	}
	for _, r := range s.GetRecommendations() {
		out.Recommendations = append(out.Recommendations, domain.SolutionRecommendation{
			Title: r.GetTitle(), Description: r.GetDescription(), Priority: r.GetPriority(), References: fromRefs(r.GetReferences()),
		})
	}
	for _, i := range s.GetSuccessIndicators() {
		out.SuccessIndicators = append(out.SuccessIndicators, domain.SolutionIndicator{
			Metric: i.GetMetric(), Target: i.GetTarget(), Timeframe: i.GetTimeframe(),
		})
	}
	return out
}

func toRefs(r domain.References) *aiv1.References {
	return &aiv1.References{
		OpinionIds:      uuidStrings(r.OpinionIDs),
		OwnerMessageIds: uuidStrings(r.OwnerMessageIDs),
		MemoryIds:       uuidStrings(r.MemoryIDs),
	}
}

func fromRefs(r *aiv1.References) domain.References {
	return domain.References{
		OpinionIDs:      parseUUIDs(r.GetOpinionIds()),
		OwnerMessageIDs: parseUUIDs(r.GetOwnerMessageIds()),
		MemoryIDs:       parseUUIDs(r.GetMemoryIds()),
	}
}

// --- enums ---------------------------------------------------------------------------

func toSentiment(s domain.Sentiment) aiv1.Sentiment {
	switch s {
	case domain.SentimentNegative:
		return aiv1.Sentiment_SENTIMENT_NEGATIVE
	case domain.SentimentPositive:
		return aiv1.Sentiment_SENTIMENT_POSITIVE
	case domain.SentimentNeutral:
		return aiv1.Sentiment_SENTIMENT_NEUTRAL
	default:
		return aiv1.Sentiment_SENTIMENT_UNSPECIFIED
	}
}

// fromSentiment returns "" for an unspecified or unknown sentiment.
func fromSentiment(s aiv1.Sentiment) domain.Sentiment {
	switch s {
	case aiv1.Sentiment_SENTIMENT_NEGATIVE:
		return domain.SentimentNegative
	case aiv1.Sentiment_SENTIMENT_POSITIVE:
		return domain.SentimentPositive
	case aiv1.Sentiment_SENTIMENT_NEUTRAL:
		return domain.SentimentNeutral
	default:
		return ""
	}
}

func toSeverity(s *domain.Severity) aiv1.Severity {
	if s != nil && *s == domain.SeveritySevere {
		return aiv1.Severity_SEVERITY_SEVERE
	}
	return aiv1.Severity_SEVERITY_NOT_SEVERE
}

func toEvent(e domain.TurnEvent) aiv1.ConsultationEvent {
	switch e {
	case domain.EventStart:
		return aiv1.ConsultationEvent_CONSULTATION_EVENT_START
	case domain.EventOwnerReply:
		return aiv1.ConsultationEvent_CONSULTATION_EVENT_OWNER_REPLY
	case domain.EventSkipQuestion:
		return aiv1.ConsultationEvent_CONSULTATION_EVENT_SKIP_QUESTION
	case domain.EventGenerateNow:
		return aiv1.ConsultationEvent_CONSULTATION_EVENT_GENERATE_NOW
	case domain.EventContinueDiscussion:
		return aiv1.ConsultationEvent_CONSULTATION_EVENT_CONTINUE_DISCUSSION
	default:
		return aiv1.ConsultationEvent_CONSULTATION_EVENT_UNSPECIFIED
	}
}

func toRole(r domain.MessageRole) aiv1.MessageRole {
	switch r {
	case domain.RoleOwner:
		return aiv1.MessageRole_MESSAGE_ROLE_OWNER
	case domain.RoleAtlasAI:
		return aiv1.MessageRole_MESSAGE_ROLE_ATLAS_AI
	default:
		return aiv1.MessageRole_MESSAGE_ROLE_UNSPECIFIED
	}
}

// --- helpers -------------------------------------------------------------------------

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func optionalTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// parseUUID maps an unparsable ID to uuid.Nil, which no snapshot or session contains, so
// reference validation rejects it.
func parseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func parseUUIDs(in []string) []uuid.UUID {
	out := make([]uuid.UUID, len(in))
	for i, s := range in {
		out[i] = parseUUID(s)
	}
	return out
}

func parseOptionalUUID(s string) *uuid.UUID {
	if s == "" {
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &id
}
