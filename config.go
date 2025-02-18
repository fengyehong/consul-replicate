// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/hashicorp/consul-template/config"
	"github.com/hashicorp/consul-template/signals"
	"github.com/hashicorp/hcl"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
)

const (
	// DefaultLogLevel is the default logging level.
	DefaultLogLevel = "WARN"

	// DefaultMaxStale is the default staleness permitted. This enables stale
	// queries by default for performance reasons.
	DefaultMaxStale = 2 * time.Second

	// DefaultReloadSignal is the default signal for reload.
	DefaultReloadSignal = syscall.SIGHUP

	// DefaultKillSignal is the default signal for termination.
	DefaultKillSignal = syscall.SIGINT

	// DefaultStatusDir is the default directory to post status information.
	DefaultStatusDir = "service/consul-replicate/statuses"
)

// Config is used to configure Consul ENV
type Config struct {
	// Consul is the configuration for connecting to a Consul cluster.
	Consul *config.ConsulConfig `mapstructure:"consul"`

	DestinationConsul *config.ConsulConfig `mapstructure:"consul"`

	// Excludes is the list of key prefixes to exclude from replication.
	Excludes *ExcludeConfigs `mapstructure:"exclude"`

	// KillSignal is the signal to listen for a graceful terminate event.
	KillSignal *os.Signal `mapstructure:"kill_signal"`

	// LogLevel is the level with which to log for this config.
	LogLevel *string `mapstructure:"log_level"`

	// MaxStale is the maximum amount of time for staleness from Consul as given
	// by LastContact.
	MaxStale *time.Duration `mapstructure:"max_stale"`

	// PidFile is the path on disk where a PID file should be written containing
	// this processes PID.
	PidFile *string `mapstructure:"pid_file"`

	// Prefixes is the list of key prefix dependencies.
	Prefixes *PrefixConfigs `mapstructure:"prefix"`

	// ReloadSignal is the signal to listen for a reload event.
	ReloadSignal *os.Signal `mapstructure:"reload_signal"`

	// StatusDir is the path in the KV store that is used to store the replication
	// statuses (default: "service/consul-replicate/statuses").
	StatusDir *string `mapstructure:"status_dir"`

	// Syslog is the configuration for syslog.
	Syslog *config.SyslogConfig `mapstructure:"syslog"`

	// Wait is the quiescence timers.
	Wait *config.WaitConfig `mapstructure:"wait"`
}

// Copy returns a deep copy of the current configuration. This is useful because
// the nested data structures may be shared.
func (c *Config) Copy() *Config {
	var o Config

	if c.Consul != nil {
		o.Consul = c.Consul.Copy()
	}

	if c.Excludes != nil {
		o.Excludes = c.Excludes.Copy()
	}

	o.KillSignal = c.KillSignal

	o.LogLevel = c.LogLevel

	o.MaxStale = c.MaxStale

	o.PidFile = c.PidFile

	if c.Prefixes != nil {
		o.Prefixes = c.Prefixes.Copy()
	}

	o.ReloadSignal = c.ReloadSignal

	o.StatusDir = c.StatusDir

	if c.Syslog != nil {
		o.Syslog = c.Syslog.Copy()
	}

	if c.Wait != nil {
		o.Wait = c.Wait.Copy()
	}

	return &o
}

func (c *Config) Merge(o *Config) *Config {
	if c == nil {
		if o == nil {
			return nil
		}
		return o.Copy()
	}

	if o == nil {
		return c.Copy()
	}

	r := c.Copy()

	if o.Consul != nil {
		r.Consul = r.Consul.Merge(o.Consul)
	}

	if o.Excludes != nil {
		r.Excludes = r.Excludes.Merge(o.Excludes)
	}

	if o.KillSignal != nil {
		r.KillSignal = o.KillSignal
	}

	if o.LogLevel != nil {
		r.LogLevel = o.LogLevel
	}

	if o.MaxStale != nil {
		r.MaxStale = o.MaxStale
	}

	if o.PidFile != nil {
		r.PidFile = o.PidFile
	}

	if o.Prefixes != nil {
		r.Prefixes = r.Prefixes.Merge(o.Prefixes)
	}

	if o.ReloadSignal != nil {
		r.ReloadSignal = o.ReloadSignal
	}

	if o.StatusDir != nil {
		r.StatusDir = o.StatusDir
	}

	if o.Syslog != nil {
		r.Syslog = r.Syslog.Merge(o.Syslog)
	}

	if o.Wait != nil {
		r.Wait = r.Wait.Merge(o.Wait)
	}

	return r
}

// GoString defines the printable version of this struct.
func (c *Config) GoString() string {
	if c == nil {
		return "(*Config)(nil)"
	}

	return fmt.Sprintf("&Config{"+
		"Consul:%s, "+
		"Excludes:%s, "+
		"KillSignal:%s, "+
		"LogLevel:%s, "+
		"MaxStale:%s, "+
		"PidFile:%s, "+
		"Prefixes:%s, "+
		"ReloadSignal:%s, "+
		"StatusDir:%s, "+
		"Syslog:%s, "+
		"Wait:%s"+
		"}",
		c.Consul.GoString(),
		c.Excludes.GoString(),
		config.SignalGoString(c.KillSignal),
		config.StringGoString(c.LogLevel),
		config.TimeDurationGoString(c.MaxStale),
		config.StringGoString(c.PidFile),
		c.Prefixes.GoString(),
		config.SignalGoString(c.ReloadSignal),
		config.StringGoString(c.StatusDir),
		c.Syslog.GoString(),
		c.Wait.GoString(),
	)
}

// DefaultConfig returns the default configuration struct. Certain environment
// variables may be set which control the values for the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Consul:            config.DefaultConsulConfig(),
		DestinationConsul: config.DefaultConsulConfig(),
		Excludes:          DefaultExcludeConfigs(),
		Prefixes:          DefaultPrefixConfigs(),
		StatusDir:         config.String(DefaultStatusDir),
		Syslog:            config.DefaultSyslogConfig(),
		Wait:              config.DefaultWaitConfig(),
	}
}

// Finalize ensures all configuration options have the default values, so it
// is safe to dereference the pointers later down the line. It also
// intelligently tries to activate stanzas that should be "enabled" because
// data was given, but the user did not explicitly add "Enabled: true" to the
// configuration.
func (c *Config) Finalize() {
	if c == nil {
		return
	}

	if c.Consul == nil {
		c.Consul = config.DefaultConsulConfig()
	}
	c.Consul.Finalize()

	if c.Excludes == nil {
		c.Excludes = DefaultExcludeConfigs()
	}
	c.Excludes.Finalize()

	if c.KillSignal == nil {
		c.KillSignal = config.Signal(DefaultKillSignal)
	}

	if c.LogLevel == nil {
		c.LogLevel = stringFromEnv([]string{
			"CR_LOG",
			"CONSUL_REPLICATE_LOG",
		}, DefaultLogLevel)
	}

	if c.MaxStale == nil {
		c.MaxStale = config.TimeDuration(DefaultMaxStale)
	}

	if c.Prefixes == nil {
		c.Prefixes = DefaultPrefixConfigs()
	}
	c.Prefixes.Finalize()

	if c.PidFile == nil {
		c.PidFile = config.String("")
	}

	if c.ReloadSignal == nil {
		c.ReloadSignal = config.Signal(DefaultReloadSignal)
	}

	if c.StatusDir == nil {
		c.StatusDir = config.String(DefaultStatusDir)
	}

	if c.Syslog == nil {
		c.Syslog = config.DefaultSyslogConfig()
	}
	c.Syslog.Finalize()

	if c.Wait == nil {
		c.Wait = config.DefaultWaitConfig()
	}
	c.Wait.Finalize()
}

// Parse parses the given string contents as a config
func Parse(s string) (*Config, error) {
	var shadow interface{}
	if err := hcl.Decode(&shadow, s); err != nil {
		return nil, errors.Wrap(err, "error decoding config")
	}

	// Convert to a map and flatten the keys we want to flatten
	parsed, ok := shadow.(map[string]interface{})
	if !ok {
		return nil, errors.New("error converting config")
	}

	flattenKeys(parsed, []string{
		"consul",
		"consul.auth",
		"consul.retry",
		"consul.ssl",
		"consul.transport",
		"syslog",
		"wait",
	})

	// Deprecations
	// TODO remove in 0.5.0
	flattenKeys(parsed, []string{
		"auth",
		"ssl",
	})
	if auth, ok := parsed["auth"]; ok {
		log.Printf("[WARN] auth is now a child stanza inside consul instead of a " +
			"top-level stanza. Update your configuration files and change auth {} " +
			"to consul { auth { ... } } instead.")
		consul, ok := parsed["consul"].(map[string]interface{})
		if !ok {
			consul = map[string]interface{}{}
		}
		consul["auth"] = auth
		parsed["consul"] = consul
		delete(parsed, "auth")
	}
	if _, ok := parsed["path"]; ok {
		log.Printf("[WARN] path is no longer a key in the configuration. Please " +
			"remove it and use the CLI option instead.")
		delete(parsed, "path")
	}
	if retry, ok := parsed["retry"]; ok {
		log.Printf("[WARN] retry is now a child stanza for consul instead of a " +
			"top-level stanza. Update your configuration files and change retry {} " +
			"to consul { retry { ... } } instead.")

		consul, ok := parsed["consul"].(map[string]interface{})
		if !ok {
			consul = map[string]interface{}{}
		}

		r := map[string]interface{}{
			"backoff":     retry,
			"max_backoff": retry,
		}

		consul["retry"] = r
		parsed["consul"] = consul

		delete(parsed, "retry")
	}
	if ssl, ok := parsed["ssl"]; ok {
		log.Printf("[WARN] ssl is now a child stanza for consul instead of a " +
			"top-level stanza. Update your configuration files and change ssl {} " +
			"to consul { ssl { ... } } instead.")

		consul, ok := parsed["consul"].(map[string]interface{})
		if !ok {
			consul = map[string]interface{}{}
		}

		consul["ssl"] = ssl
		parsed["consul"] = consul

		delete(parsed, "ssl")
	}
	if token, ok := parsed["token"]; ok {
		log.Printf("[WARN] token is now a child stanza inside consul instead of a " +
			"top-level key. Update your configuration files and change token = \"...\" " +
			"to consul { token = \"...\" } instead.")
		consul, ok := parsed["consul"].(map[string]interface{})
		if !ok {
			consul = map[string]interface{}{}
		}
		consul["token"] = token
		parsed["consul"] = consul
		delete(parsed, "token")
	}

	// Create a new, empty config
	var c Config

	// Use mapstructure to populate the basic config fields
	var md mapstructure.Metadata
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			StringToPrefixConfigFunc(),
			MapToPrefixConfigFunc(),
			StringToExcludeConfigFunc(),
			config.ConsulStringToStructFunc(),
			config.StringToFileModeFunc(),
			signals.StringToSignalFunc(),
			config.StringToWaitDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
			mapstructure.StringToTimeDurationHookFunc(),
		),
		ErrorUnused: true,
		Metadata:    &md,
		Result:      &c,
	})
	if err != nil {
		return nil, errors.Wrap(err, "mapstructure decoder creation failed")
	}
	if err := decoder.Decode(parsed); err != nil {
		return nil, errors.Wrap(err, "mapstructure decode failed")
	}

	return &c, nil
}

// Must returns a config object that must compile. If there are any errors, this
// function will panic. This is most useful in testing or constants.
func Must(s string) *Config {
	c, err := Parse(s)
	if err != nil {
		log.Fatal(err)
	}
	return c
}

// TestConfig returns a default, finalized config, with the provided
// configuration taking precedence.
func TestConfig(c *Config) *Config {
	d := DefaultConfig().Merge(c)
	d.Finalize()
	return d
}

// FromFile reads the configuration file at the given path and returns a new
// Config struct with the data populated.
func FromFile(path string) (*Config, error) {
	c, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "from file: "+path)
	}

	config, err := Parse(string(c))
	if err != nil {
		return nil, errors.Wrap(err, "from file: "+path)
	}
	return config, nil
}

// FromPath iterates and merges all configuration files in a given
// directory, returning the resulting config.
func FromPath(path string) (*Config, error) {
	// Ensure the given filepath exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, errors.Wrap(err, "missing file/folder: "+path)
	}

	// Check if a file was given or a path to a directory
	stat, err := os.Stat(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed stating file: "+path)
	}

	// Recursively parse directories, single load files
	if stat.Mode().IsDir() {
		// Ensure the given filepath has at least one config file
		_, err := os.ReadDir(path)
		if err != nil {
			return nil, errors.Wrap(err, "failed listing dir: "+path)
		}

		// Create a blank config to merge off of
		var c *Config

		// Potential bug: Walk does not follow symlinks!
		err = filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			// If WalkFunc had an error, just return it
			if err != nil {
				return err
			}

			// Do nothing for directories
			if info.IsDir() {
				return nil
			}

			// Parse and merge the config
			newConfig, err := FromFile(path)
			if err != nil {
				return err
			}
			c = c.Merge(newConfig)

			return nil
		})

		if err != nil {
			return nil, errors.Wrap(err, "walk error")
		}

		return c, nil
	} else if stat.Mode().IsRegular() {
		return FromFile(path)
	}

	return nil, fmt.Errorf("unknown filetype: %q", stat.Mode().String())
}

func stringFromEnv(list []string, def string) *string {
	for _, s := range list {
		if v := os.Getenv(s); v != "" {
			return config.String(strings.TrimSpace(v))
		}
	}
	return config.String(def)
}

// flattenKeys is a function that takes a map[string]interface{} and recursively
// flattens any keys that are a []map[string]interface{} where the key is in the
// given list of keys.
func flattenKeys(m map[string]interface{}, keys []string) {
	keyMap := make(map[string]struct{})
	for _, key := range keys {
		keyMap[key] = struct{}{}
	}

	var flatten func(map[string]interface{}, string)
	flatten = func(m map[string]interface{}, parent string) {
		for k, v := range m {
			// Calculate the map key, since it could include a parent.
			mapKey := k
			if parent != "" {
				mapKey = parent + "." + k
			}

			if _, ok := keyMap[mapKey]; !ok {
				continue
			}

			switch typed := v.(type) {
			case []map[string]interface{}:
				if len(typed) > 0 {
					last := typed[len(typed)-1]
					flatten(last, mapKey)
					m[k] = last
				} else {
					m[k] = nil
				}
			case map[string]interface{}:
				flatten(typed, mapKey)
				m[k] = typed
			default:
				m[k] = v
			}
		}
	}

	flatten(m, "")
}

// regexpMatch matches the given regexp and extracts the match groups into a
// named map.
func regexpMatch(re *regexp.Regexp, q string) map[string]string {
	names := re.SubexpNames()
	match := re.FindAllStringSubmatch(q, -1)

	if len(match) == 0 {
		return map[string]string{}
	}

	m := map[string]string{}
	for i, n := range match[0] {
		if names[i] != "" {
			m[names[i]] = n
		}
	}

	return m
}
