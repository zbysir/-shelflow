package llms

// CallOption is a function that configures a CallOptions.
type CallOption func(*CallOptions)

// CallOptions is a set of options for LLM.Call.
type CallOptions struct {
	// Model is the model to use.
	Model string `json:"model"`
	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens int `json:"max_tokens"`
	// Temperature is the temperature for sampling, between 0 and 1.
	Temperature float64 `json:"temperature"`
	// StopWords is a list of words to stop on.
	StopWords []string `json:"stop_words"`
}

// WithModel is an option for LLM.Call.
func WithModel(model string) CallOption {
	return func(o *CallOptions) {
		o.Model = model
	}
}

// WithMaxTokens is an option for LLM.Call.
func WithMaxTokens(maxTokens int) CallOption {
	return func(o *CallOptions) {
		o.MaxTokens = maxTokens
	}
}

// WithTemperature is an option for LLM.Call.
func WithTemperature(temperature float64) CallOption {
	return func(o *CallOptions) {
		o.Temperature = temperature
	}
}

// WithStopWords is an option for LLM.Call.
func WithStopWords(stopWords []string) CallOption {
	return func(o *CallOptions) {
		o.StopWords = stopWords
	}
}

// WithOptions is an option for LLM.Call.
func WithOptions(options CallOptions) CallOption {
	return func(o *CallOptions) {
		(*o) = options
	}
}
