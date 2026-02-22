package engine

// DetectConfig holds parameters for backend detection.
type DetectConfig struct {
	OllamaBaseURL string
}

// Detect probes available local inference backends and returns the best one.
// Currently always returns an OllamaEngine; future: probe for MLX server and
// return MLXEngine if available.
func Detect(cfg DetectConfig) (Engine, error) {
	return NewOllamaEngine(cfg.OllamaBaseURL), nil
}
