package service

import (
	"errors"
	"testing"
	"time"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
)

func TestPlanetStatus(t *testing.T) {
	archived := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	before, after := archived.Add(-time.Hour), archived.Add(time.Hour)
	if got := PlanetStatus(&before, archived); got != domain.PlanetConsulted {
		t.Errorf("old opinions: %s", got)
	}
	if got := PlanetStatus(&after, archived); got != domain.PlanetHasNewReviews {
		t.Errorf("new opinions: %s", got)
	}
	if got := PlanetStatus(nil, archived); got != domain.PlanetConsulted {
		t.Errorf("no opinions: %s", got)
	}
}

func TestDashboardRange(t *testing.T) {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	// 2026-11-12 01:00 in Jakarta is still 2026-11-11 in UTC.
	now := time.Date(2026, 11, 11, 18, 0, 0, 0, time.UTC)
	svc := &InsightService{now: func() time.Time { return now }}
	day := func(s string) *time.Time { d, _ := time.Parse(dateLayout, s); return &d }

	tests := []struct {
		name     string
		in       DashboardInput
		wantFrom string
		wantTo   string
		wantErr  error
	}{
		{name: "default is the last 30 local days", in: DashboardInput{}, wantFrom: "2026-10-14", wantTo: "2026-11-13"},
		{name: "explicit inclusive range", in: DashboardInput{From: day("2026-11-01"), To: day("2026-11-30"), TimeZone: "UTC"}, wantFrom: "2026-11-01", wantTo: "2026-12-01"},
		{name: "from after to", in: DashboardInput{From: day("2026-11-02"), To: day("2026-11-01")}, wantErr: apperror.ErrInvalidInput},
		{name: "range too long", in: DashboardInput{From: day("2025-01-01"), To: day("2026-11-01")}, wantErr: apperror.ErrInvalidInput},
		{name: "unknown time zone", in: DashboardInput{TimeZone: "Mars/Olympus"}, wantErr: apperror.ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rg, _, err := svc.dashboardRange(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if got := rg.From.Format(dateLayout); got != tt.wantFrom {
				t.Errorf("from = %s, want %s", got, tt.wantFrom)
			}
			if got := rg.To.Format(dateLayout); got != tt.wantTo {
				t.Errorf("to = %s, want %s", got, tt.wantTo)
			}
			if tt.in.TimeZone == "" && rg.From.Location().String() != jakarta.String() {
				t.Errorf("default zone = %s", rg.From.Location())
			}
		})
	}
}

func TestFillDaysAndWeeks(t *testing.T) {
	loc := time.UTC
	rg := domain.DashboardRange{
		From: time.Date(2026, 11, 4, 0, 0, 0, 0, loc), // Wednesday
		To:   time.Date(2026, 11, 11, 0, 0, 0, 0, loc),
	}
	days := fillDays([]domain.SentimentTrendPoint{
		{Date: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), Sentiment: domain.SentimentBreakdown{Negative: 2}},
	}, rg, loc)
	if len(days) != 7 || days[1].Sentiment.Negative != 2 || days[0].Sentiment.Negative != 0 {
		t.Errorf("days = %+v", days)
	}
	weeks := fillWeeks([]domain.WeeklyReviewBucket{
		{WeekStart: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), FeedbackCount: 4},
	}, rg, loc)
	if len(weeks) != 2 || weeks[0].WeekStart.Format(dateLayout) != "2026-11-02" || weeks[1].FeedbackCount != 4 {
		t.Errorf("weeks = %+v", weeks)
	}
}
