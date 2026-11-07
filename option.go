package dbfwd

import (
	"maps"
	"os"
)

// Option sets one value of the Config of a call of [Resolve] or [Lookup].
type Option func(*Config)

// Config is the configuration of one call, built from its options. A caller
// never builds one. A backend reads it.
type Config struct {
	// Tools maps the name of a tool, such as kubectl, to its path.
	Tools map[string]string

	// Env holds the variables that WithEnv set, as KEY=value, in order.
	Env []string

	// Interactive runs a child process with the terminal, or is nil.
	Interactive func(run func(tty *os.File) error) error
}

// Tool returns the path of the tool name, or name itself when no option set
// a path, so that exec looks for it on the PATH.
func (cfg Config) Tool(name string) string {
	if p, ok := cfg.Tools[name]; ok && p != "" {
		return p
	}
	return name
}

// newConfig builds a Config from opts, applied in order.
func newConfig(opts []Option) Config {
	var cfg Config
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	return cfg
}

// WithTool runs the tool name from path, such as WithTool("kubectl",
// "/opt/bin/kubectl"). With no such option, a backend looks for the tool on
// the PATH.
func WithTool(name, path string) Option {
	return func(cfg *Config) {
		tools := make(map[string]string, len(cfg.Tools)+1)
		maps.Copy(tools, cfg.Tools)
		tools[name] = path
		cfg.Tools = tools
	}
}

// WithEnv sets the environment variable key to value for every child process
// of the call, such as WithEnv("KUBECONFIG", path). With no such option, a
// child process gets the environment of the caller.
func WithEnv(key, value string) Option {
	return func(cfg *Config) {
		cfg.Env = append(append([]string(nil), cfg.Env...), key+"="+value)
	}
}

// WithInteractive runs each child process that can prompt inside fn, so that
// a credential plugin or a login can use the terminal. With no such option,
// no child process gets a terminal.
func WithInteractive(fn func(run func(tty *os.File) error) error) Option {
	return func(cfg *Config) {
		cfg.Interactive = fn
	}
}
