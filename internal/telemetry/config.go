package telemetry

import (
	"os"
	"strconv"
	"strings"
)

// Config controls OpenTelemetry export and service identity.
type Config struct {
	Enabled     bool
	ServiceName string
	// OTLPEndpoint is the OTLP HTTP collector base URL, e.g. http://jaeger:4318
	OTLPEndpoint string
	Insecure     bool
	SampleRatio  float64
}

// LoadConfigFromEnv reads standard OTEL_* variables.
func LoadConfigFromEnv(serviceName string) Config {
	enabled := os.Getenv("OTEL_ENABLED") == "true"
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:4318"
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	ratio := 1.0
	if raw := os.Getenv("OTEL_TRACES_SAMPLER_ARG"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v >= 0 && v <= 1 {
			ratio = v
		}
	}
	insecure := os.Getenv("OTEL_EXPORTER_OTLP_INSECURE") != "false"
	if name := os.Getenv("OTEL_SERVICE_NAME"); name != "" {
		serviceName = name
	}
	return Config{
		Enabled:      enabled,
		ServiceName:  serviceName,
		OTLPEndpoint: endpoint,
		Insecure:     insecure,
		SampleRatio:  ratio,
	}
}
