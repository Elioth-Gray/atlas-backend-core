// Package aiclient implements service.AIClient on top of the generated atlas.ai.v1 gRPC
// client. It only translates between protobuf and service types; all validation of AI
// output happens in the services.
package aiclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	aiv1 "atlas-backend-core/gen/atlas/ai/v1"
	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/service"
)

// Client calls the Python AI service.
type Client struct {
	rpc                 aiv1.AIServiceClient
	feedbackTimeout     time.Duration
	consultationTimeout time.Duration
}

// New wraps a connection to the AI service. Each RPC is bounded by its timeout (or by an
// earlier deadline already on the context).
func New(conn grpc.ClientConnInterface, feedbackTimeout, consultationTimeout time.Duration) *Client {
	return &Client{
		rpc:                 aiv1.NewAIServiceClient(conn),
		feedbackTimeout:     feedbackTimeout,
		consultationTimeout: consultationTimeout,
	}
}

var _ service.AIClient = (*Client)(nil)

// ProcessFeedback implements service.AIClient.
func (c *Client) ProcessFeedback(ctx context.Context, req service.FeedbackAnalysisRequest) (service.FeedbackAnalysis, error) {
	ctx, cancel := context.WithTimeout(ctx, c.feedbackTimeout)
	defer cancel()
	resp, err := c.rpc.ProcessFeedback(ctx, toFeedbackRequest(req))
	if err != nil {
		return service.FeedbackAnalysis{}, translate("ProcessFeedback", err)
	}
	return fromFeedbackResponse(resp), nil
}

// ProcessConsultationTurn implements service.AIClient.
func (c *Client) ProcessConsultationTurn(ctx context.Context, req service.ConsultationTurnRequest) (service.ConsultationTurnResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.consultationTimeout)
	defer cancel()
	resp, err := c.rpc.ProcessConsultationTurn(ctx, toTurnRequest(req))
	if err != nil {
		return service.ConsultationTurnResult{}, translate("ProcessConsultationTurn", err)
	}
	return fromTurnResponse(resp), nil
}

// translate turns a gRPC failure into a client-safe apperror. Cancellation is passed
// through unchanged so callers can tell a gone client from a broken AI service.
func translate(method string, err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	switch status.Code(err) {
	case codes.Canceled:
		return fmt.Errorf("ai %s: %w", method, context.Canceled)
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Aborted:
		return apperror.Wrap(apperror.ErrUnavailable, "the AI service is temporarily unavailable, please try again", fmt.Errorf("ai %s: %w", method, err))
	default:
		return apperror.Wrap(apperror.ErrUnavailable, "the AI service could not process the request", fmt.Errorf("ai %s: %w", method, err))
	}
}

// Disabled is the AIClient used when no AI service is configured.
type Disabled struct{}

var _ service.AIClient = Disabled{}

// ProcessFeedback implements service.AIClient.
func (Disabled) ProcessFeedback(context.Context, service.FeedbackAnalysisRequest) (service.FeedbackAnalysis, error) {
	return service.FeedbackAnalysis{}, apperror.Unavailable("the AI service is not configured")
}

// ProcessConsultationTurn implements service.AIClient.
func (Disabled) ProcessConsultationTurn(context.Context, service.ConsultationTurnRequest) (service.ConsultationTurnResult, error) {
	return service.ConsultationTurnResult{}, apperror.Unavailable("the AI service is not configured")
}
