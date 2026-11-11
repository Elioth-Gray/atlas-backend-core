// Package config loads and validates application configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Supported values for APP_ENV.
const (
	EnvDevelopment = "development"
	EnvStaging     = "staging"
	EnvProduction  = "production"
	EnvTest        = "test"
)

// Supported values for LOG_FORMAT.
const (
	LogFormatJSON = "json"
	LogFormatText = "text"
)

var (
	validEnvs      = []string{EnvDevelopment, EnvStaging, EnvProduction, EnvTest}
	validLogLevels = []string{"debug", "info", "warn", "error"}
)

// minProductionGRPCTokenLength is the shortest GRPC_AUTH_TOKEN accepted in production.
const minProductionGRPCTokenLength = 32

// Config is the root application configuration.
type Config struct {
	App  AppConfig
	HTTP HTTPConfig
	GRPC GRPCConfig
	CORS CORSConfig
	DB   DBConfig
	Log  LogConfig
}

// AppConfig holds general application settings.
type AppConfig struct {
	Name string `env:"APP_NAME" envDefault:"atlas-backend-core"`
	Env  string `env:"APP_ENV" envDefault:"development"`
}

// IsProduction reports whether the application runs in the production environment.
func (c AppConfig) IsProduction() bool { return c.Env == EnvProduction }

// HTTPConfig holds HTTP server settings.
type HTTPConfig struct {
	Host              string        `env:"HTTP_HOST"`
	Port              int           `env:"HTTP_PORT" envDefault:"8080"`
	ReadTimeout       time.Duration `env:"HTTP_READ_TIMEOUT" envDefault:"10s"`
	ReadHeaderTimeout time.Duration `env:"HTTP_READ_HEADER_TIMEOUT" envDefault:"5s"`
	WriteTimeout      time.Duration `env:"HTTP_WRITE_TIMEOUT" envDefault:"15s"`
	IdleTimeout       time.Duration `env:"HTTP_IDLE_TIMEOUT" envDefault:"60s"`
	ShutdownTimeout   time.Duration `env:"HTTP_SHUTDOWN_TIMEOUT" envDefault:"15s"`
	MaxBodyBytes      int64         `env:"HTTP_MAX_BODY_BYTES" envDefault:"1048576"`
	TrustedProxies    []string      `env:"HTTP_TRUSTED_PROXIES" envSeparator:","`
}

// Addr returns the host:port address the HTTP server listens on.
func (c HTTPConfig) Addr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

// GRPCConfig holds settings for the internal gRPC server used for service-to-service
// calls. AuthToken is the shared secret other backends must send as
// "authorization: Bearer <token>"; an empty token disables auth (development only).
type GRPCConfig struct {
	Enabled          bool          `env:"GRPC_ENABLED" envDefault:"true"`
	Host             string        `env:"GRPC_HOST"`
	Port             int           `env:"GRPC_PORT" envDefault:"9090"`
	AuthToken        string        `env:"GRPC_AUTH_TOKEN"`
	TLSCertFile      string        `env:"GRPC_TLS_CERT_FILE"`
	TLSKeyFile       string        `env:"GRPC_TLS_KEY_FILE"`
	MaxRecvMsgBytes  int           `env:"GRPC_MAX_RECV_MSG_BYTES" envDefault:"4194304"`
	MaxSendMsgBytes  int           `env:"GRPC_MAX_SEND_MSG_BYTES" envDefault:"4194304"`
	KeepaliveTime    time.Duration `env:"GRPC_KEEPALIVE_TIME" envDefault:"2m"`
	KeepaliveTimeout time.Duration `env:"GRPC_KEEPALIVE_TIMEOUT" envDefault:"20s"`
	ShutdownTimeout  time.Duration `env:"GRPC_SHUTDOWN_TIMEOUT" envDefault:"15s"`
}

// Addr returns the host:port address the gRPC server listens on.
func (c GRPCConfig) Addr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

// CORSConfig holds cross-origin resource sharing settings. CORS is disabled when
// AllowedOrigins is empty.
type CORSConfig struct {
	AllowedOrigins   []string      `env:"CORS_ALLOWED_ORIGINS" envSeparator:","`
	AllowCredentials bool          `env:"CORS_ALLOW_CREDENTIALS" envDefault:"false"`
	MaxAge           time.Duration `env:"CORS_MAX_AGE" envDefault:"12h"`
}

// DBConfig holds PostgreSQL connection and GORM settings.
type DBConfig struct {
	URL                string        `env:"DATABASE_URL,required,notEmpty"`
	MaxOpenConns       int           `env:"DB_MAX_OPEN_CONNS" envDefault:"10"`
	MaxIdleConns       int           `env:"DB_MAX_IDLE_CONNS" envDefault:"5"`
	MaxConnLifetime    time.Duration `env:"DB_MAX_CONN_LIFETIME" envDefault:"1h"`
	MaxConnIdleTime    time.Duration `env:"DB_MAX_CONN_IDLE_TIME" envDefault:"30m"`
	ConnectTimeout     time.Duration `env:"DB_CONNECT_TIMEOUT" envDefault:"5s"`
	SlowQueryThreshold time.Duration `env:"DB_SLOW_QUERY_THRESHOLD" envDefault:"200ms"`
	LogQueries         bool          `env:"DB_LOG_QUERIES" envDefault:"false"`
	AutoMigrate        bool          `env:"DB_AUTO_MIGRATE" envDefault:"false"`
}

// LogConfig holds structured logging settings. An empty Format resolves to JSON in
// production and staging, and to text everywhere else.
type LogConfig struct {
	Level  string `env:"LOG_LEVEL" envDefault:"info"`
	Format string `env:"LOG_FORMAT"`
}

// Load reads configuration from the process environment, seeded by an optional .env file
// in the working directory. Variables already set in the environment take precedence over
// values from .env.
func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load .env file: %w", err)
	}
	return FromMap(env.ToMap(os.Environ()))
}

// FromMap builds and validates a Config from the given key/value environment.
func FromMap(environ map[string]string) (*Config, error) {
	var cfg Config
	if err := env.ParseWithOptions(&cfg, env.Options{Environment: environ}); err != nil {
		return nil, fmt.Errorf("parse environment: %w", err)
	}
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	return &cfg, nil
}

func (c *Config) normalize() {
	c.App.Env = strings.ToLower(strings.TrimSpace(c.App.Env))
	c.Log.Level = strings.ToLower(strings.TrimSpace(c.Log.Level))
	c.Log.Format = strings.ToLower(strings.TrimSpace(c.Log.Format))
	if c.Log.Format == "" {
		c.Log.Format = LogFormatText
		if c.App.Env == EnvProduction || c.App.Env == EnvStaging {
			c.Log.Format = LogFormatJSON
		}
	}
	c.HTTP.TrustedProxies = compact(c.HTTP.TrustedProxies)
	c.CORS.AllowedOrigins = compact(c.CORS.AllowedOrigins)
}

// Validate reports every invalid setting at once.
func (c *Config) Validate() error {
	var errs []error
	if !slices.Contains(validEnvs, c.App.Env) {
		errs = append(errs, fmt.Errorf("APP_ENV must be one of %s, got %q", strings.Join(validEnvs, ", "), c.App.Env))
	}
	if c.HTTP.Port < 1 || c.HTTP.Port > 65535 {
		errs = append(errs, fmt.Errorf("HTTP_PORT must be between 1 and 65535, got %d", c.HTTP.Port))
	}
	if c.HTTP.MaxBodyBytes <= 0 {
		errs = append(errs, errors.New("HTTP_MAX_BODY_BYTES must be positive"))
	}
	errs = append(errs, c.validateGRPC()...)
	if c.DB.MaxOpenConns < 1 {
		errs = append(errs, errors.New("DB_MAX_OPEN_CONNS must be at least 1"))
	}
	if c.DB.MaxIdleConns < 0 || c.DB.MaxIdleConns > c.DB.MaxOpenConns {
		errs = append(errs, errors.New("DB_MAX_IDLE_CONNS must be between 0 and DB_MAX_OPEN_CONNS"))
	}
	if !slices.Contains(validLogLevels, c.Log.Level) {
		errs = append(errs, fmt.Errorf("LOG_LEVEL must be one of %s, got %q", strings.Join(validLogLevels, ", "), c.Log.Level))
	}
	if c.Log.Format != LogFormatJSON && c.Log.Format != LogFormatText {
		errs = append(errs, fmt.Errorf("LOG_FORMAT must be json or text, got %q", c.Log.Format))
	}
	for _, origin := range c.CORS.AllowedOrigins {
		switch {
		case origin == "*":
			if c.CORS.AllowCredentials {
				errs = append(errs, errors.New(`CORS_ALLOWED_ORIGINS cannot contain "*" when CORS_ALLOW_CREDENTIALS is true`))
			}
		case !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://"):
			errs = append(errs, fmt.Errorf("CORS origin %q must start with http:// or https://", origin))
		}
	}
	return errors.Join(errs...)
}

func (c *Config) validateGRPC() []error {
	g := c.GRPC
	if !g.Enabled {
		return nil
	}
	var errs []error
	if g.Port < 1 || g.Port > 65535 {
		errs = append(errs, fmt.Errorf("GRPC_PORT must be between 1 and 65535, got %d", g.Port))
	}
	if g.Port == c.HTTP.Port {
		errs = append(errs, errors.New("GRPC_PORT must differ from HTTP_PORT"))
	}
	if (g.TLSCertFile == "") != (g.TLSKeyFile == "") {
		errs = append(errs, errors.New("GRPC_TLS_CERT_FILE and GRPC_TLS_KEY_FILE must be set together"))
	}
	if g.MaxRecvMsgBytes <= 0 || g.MaxSendMsgBytes <= 0 {
		errs = append(errs, errors.New("GRPC_MAX_RECV_MSG_BYTES and GRPC_MAX_SEND_MSG_BYTES must be positive"))
	}
	if c.App.IsProduction() && len(g.AuthToken) < minProductionGRPCTokenLength {
		errs = append(errs, fmt.Errorf("GRPC_AUTH_TOKEN must be at least %d characters in production", minProductionGRPCTokenLength))
	}
	return errs
}

// compact trims every value and drops empty ones, returning nil when nothing is left.
func compact(values []string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
