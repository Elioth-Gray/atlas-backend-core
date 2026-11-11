package config

import (
	"strings"
	"testing"
)

func TestFromMap(t *testing.T) {
	base := func(overrides map[string]string) map[string]string {
		env := map[string]string{"DATABASE_URL": "postgres://u:p@localhost:5432/db"}
		for k, v := range overrides {
			env[k] = v
		}
		return env
	}

	tests := []struct {
		name       string
		env        map[string]string
		wantErr    string
		wantFormat string
	}{
		{name: "defaults are valid", env: base(nil), wantFormat: LogFormatText},
		{
			name:       "production defaults to json logs",
			env:        base(map[string]string{"APP_ENV": "production", "GRPC_AUTH_TOKEN": strings.Repeat("x", 32), "JWT_SECRET": strings.Repeat("s", 32)}),
			wantFormat: LogFormatJSON,
		},
		{name: "missing database url", env: map[string]string{}, wantErr: "DATABASE_URL"},
		{name: "unknown env", env: base(map[string]string{"APP_ENV": "prod"}), wantErr: "APP_ENV"},
		{name: "bad port", env: base(map[string]string{"HTTP_PORT": "70000"}), wantErr: "HTTP_PORT"},
		{name: "bad log level", env: base(map[string]string{"LOG_LEVEL": "verbose"}), wantErr: "LOG_LEVEL"},
		{
			name:    "wildcard origin with credentials",
			env:     base(map[string]string{"CORS_ALLOWED_ORIGINS": "*", "CORS_ALLOW_CREDENTIALS": "true"}),
			wantErr: "CORS_ALLOWED_ORIGINS",
		},
		{
			name:    "production requires a strong grpc token",
			env:     base(map[string]string{"APP_ENV": "production", "GRPC_AUTH_TOKEN": "short"}),
			wantErr: "GRPC_AUTH_TOKEN",
		},
		{
			name:       "production with grpc disabled needs no token",
			env:        base(map[string]string{"APP_ENV": "production", "GRPC_ENABLED": "false", "JWT_SECRET": strings.Repeat("s", 32)}),
			wantFormat: LogFormatJSON,
		},
		{
			name:    "grpc and http on the same port",
			env:     base(map[string]string{"HTTP_PORT": "9090"}),
			wantErr: "GRPC_PORT must differ",
		},
		{
			name:    "grpc tls needs both files",
			env:     base(map[string]string{"GRPC_TLS_CERT_FILE": "cert.pem"}),
			wantErr: "GRPC_TLS_CERT_FILE",
		},
		{
			name:    "production requires a strong jwt secret",
			env:     base(map[string]string{"APP_ENV": "production", "GRPC_AUTH_TOKEN": strings.Repeat("x", 32)}),
			wantErr: "JWT_SECRET",
		},
		{
			name:    "ai timeout must fit in the http write timeout",
			env:     base(map[string]string{"AI_CONSULTATION_TIMEOUT": "2m", "HTTP_WRITE_TIMEOUT": "1m"}),
			wantErr: "AI_CONSULTATION_TIMEOUT",
		},
		{
			name:    "job lease must outlive the ai call",
			env:     base(map[string]string{"FEEDBACK_JOB_LEASE": "10s", "AI_FEEDBACK_TIMEOUT": "30s"}),
			wantErr: "FEEDBACK_JOB_LEASE",
		},
		{
			name:    "origin without scheme",
			env:     base(map[string]string{"CORS_ALLOWED_ORIGINS": "localhost:3000"}),
			wantErr: "must start with http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := FromMap(tt.env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Log.Format != tt.wantFormat {
				t.Errorf("log format = %q, want %q", cfg.Log.Format, tt.wantFormat)
			}
		})
	}
}

func TestFromMap_ListsAreCompacted(t *testing.T) {
	cfg, err := FromMap(map[string]string{
		"DATABASE_URL":         "postgres://localhost/db",
		"CORS_ALLOWED_ORIGINS": " http://localhost:3000 , ,https://atlas.example ",
		"HTTP_TRUSTED_PROXIES": "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"http://localhost:3000", "https://atlas.example"}
	if strings.Join(cfg.CORS.AllowedOrigins, "|") != strings.Join(want, "|") {
		t.Errorf("origins = %q, want %q", cfg.CORS.AllowedOrigins, want)
	}
	if cfg.HTTP.TrustedProxies != nil {
		t.Errorf("trusted proxies = %q, want nil", cfg.HTTP.TrustedProxies)
	}
}
