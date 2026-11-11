package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/auth"
	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/service"
)

type fakeConsultations struct {
	ConsultationService
	turnIn  service.TurnInput
	turnErr error
	calls   int
}

func (f *fakeConsultations) Turn(_ context.Context, _, sessionID uuid.UUID, in service.TurnInput) (service.SessionView, error) {
	f.calls++
	f.turnIn = in
	if f.turnErr != nil {
		return service.SessionView{}, f.turnErr
	}
	return service.SessionView{Session: domain.ConsultationSession{ID: sessionID, Status: domain.SessionCollectingContext, StateVersion: in.StateVersion + 1}}, nil
}

// withUser simulates middleware.RequireAuth.
func withUser(id uuid.UUID) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(auth.WithUserID(c.Request.Context(), id))
		c.Next()
	}
}

func TestConsultationHandler_Turn(t *testing.T) {
	sessionID := uuid.New()
	path := "/api/v1/owner/consultations/" + sessionID.String() + "/turns"
	reqID := uuid.NewString()
	tests := []struct {
		name       string
		path       string
		body       string
		svcErr     error
		wantStatus int
		wantCode   string
		wantFields []string
		wantCalls  int
	}{
		{
			name:       "valid reply",
			path:       path,
			body:       `{"request_id":"` + reqID + `","state_version":2,"event":"OWNER_REPLY","content":"Dua barista"}`,
			wantStatus: http.StatusOK, wantCalls: 1,
		},
		{
			name: "unknown event and missing fields", path: path, body: `{"event":"DROP_TABLE"}`,
			wantStatus: http.StatusBadRequest, wantCode: httpx.CodeValidation, wantFields: []string{"request_id", "state_version", "event"},
		},
		{
			name: "invalid session id", path: "/api/v1/owner/consultations/abc/turns", body: `{}`,
			wantStatus: http.StatusBadRequest, wantCode: httpx.CodeValidation, wantFields: []string{"sessionId"},
		},
		{
			name: "busy session maps to 409", path: path,
			body:   `{"request_id":"` + reqID + `","state_version":2,"event":"GENERATE_NOW"}`,
			svcErr: apperror.Conflict("the session is still processing a previous request"), wantStatus: http.StatusConflict, wantCode: httpx.CodeConflict, wantCalls: 1,
		},
		{
			name: "ai outage maps to 503", path: path,
			body:   `{"request_id":"` + reqID + `","state_version":2,"event":"GENERATE_NOW"}`,
			svcErr: apperror.Unavailable("the AI service is temporarily unavailable"), wantStatus: http.StatusServiceUnavailable, wantCode: httpx.CodeUnavailable, wantCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeConsultations{turnErr: tt.svcErr}
			r := gin.New()
			r.POST("/api/v1/owner/consultations/:sessionId/turns", withUser(uuid.New()), NewConsultationHandler(svc, nil).Turn)

			w := doRequest(t, r, http.MethodPost, tt.path, tt.body)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body)
			}
			if svc.calls != tt.wantCalls {
				t.Errorf("service called %d times, want %d", svc.calls, tt.wantCalls)
			}
			if tt.wantCode == "" {
				var resp httpx.DataResponse[sessionResponse]
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if resp.Data.StateVersion != 3 || svc.turnIn.Event != domain.EventOwnerReply || svc.turnIn.Content != "Dua barista" {
					t.Errorf("unexpected mapping: resp=%+v in=%+v", resp.Data.StateVersion, svc.turnIn)
				}
				return
			}
			apiErr := decodeError(t, w)
			if apiErr.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", apiErr.Code, tt.wantCode)
			}
			assertFields(t, apiErr.Details, tt.wantFields)
		})
	}
}

type fakeFeedbacks struct {
	in    service.SubmitFeedbackInput
	calls int
}

func (f *fakeFeedbacks) Submit(_ context.Context, shopID uuid.UUID, in service.SubmitFeedbackInput) (domain.Feedback, error) {
	f.calls++
	f.in = in
	return domain.Feedback{ID: uuid.New(), ShopID: shopID, Rating: in.Rating}, nil
}

func TestShopHandler_SubmitFeedback(t *testing.T) {
	shopID := uuid.NewString()
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantFields []string
	}{
		{name: "valid", body: `{"name":"Budi","rating":4.5,"feedback":"Enak"}`, wantStatus: http.StatusCreated},
		{name: "rating out of range", body: `{"rating":5.5,"feedback":"x"}`, wantStatus: http.StatusBadRequest, wantFields: []string{"rating"}},
		{name: "missing rating and text", body: `{}`, wantStatus: http.StatusBadRequest, wantFields: []string{"rating", "feedback"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeFeedbacks{}
			r := gin.New()
			r.POST("/shops/:shopId/feedbacks", NewShopHandler(nil, svc).SubmitFeedback)

			w := doRequest(t, r, http.MethodPost, "/shops/"+shopID+"/feedbacks", tt.body)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body)
			}
			if tt.wantStatus == http.StatusCreated {
				if svc.in.Rating != 4.5 || svc.in.Text != "Enak" {
					t.Errorf("input = %+v", svc.in)
				}
				return
			}
			if svc.calls != 0 {
				t.Error("service must not be called for invalid input")
			}
			assertFields(t, decodeError(t, w).Details, tt.wantFields)
		})
	}
}

func assertFields(t *testing.T, details []httpx.FieldError, want []string) {
	t.Helper()
	got := make(map[string]bool, len(details))
	for _, d := range details {
		got[d.Field] = true
	}
	for _, f := range want {
		if !got[f] {
			t.Errorf("missing validation detail for field %q in %+v", f, details)
		}
	}
}
