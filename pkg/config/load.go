package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Load uses a fresh Viper instance. Only an explicitly provided file is read;
// there is no working-directory or home-directory discovery. Changed flags beat
// LANTERN_* environment variables, which beat file values, which beat defaults.
// A requested missing file, unknown key, or invalid value is an error.
func Load(path string, flags *pflag.FlagSet) (Config, error) {
	v := viper.New()
	v.SetEnvPrefix("LANTERN")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	v.AllowEmptyEnv(true)
	defaults := Defaults()
	settings := map[string]any{
		"dns.address":             defaults.DNS.Address,
		"dns.read_timeout":        defaults.DNS.ReadTimeout,
		"dns.write_timeout":       defaults.DNS.WriteTimeout,
		"dns.handler_timeout":     defaults.DNS.HandlerTimeout,
		"dns.concurrency":         defaults.DNS.Concurrency,
		"web.address":             defaults.Web.Address,
		"web.allowed_hosts":       defaults.Web.AllowedHosts,
		"web.read_header_timeout": defaults.Web.ReadHeaderTimeout,
		"web.read_timeout":        defaults.Web.ReadTimeout,
		"web.write_timeout":       defaults.Web.WriteTimeout,
		"web.idle_timeout":        defaults.Web.IdleTimeout,
		"web.max_header_bytes":    defaults.Web.MaxHeaderBytes,
		"shutdown_timeout":        defaults.ShutdownTimeout,
	}
	for key, value := range settings {
		v.SetDefault(key, value)
		// Explicit binding keeps environment-only keys visible to UnmarshalExact.
		if err := v.BindEnv(key); err != nil {
			return Config{}, err
		}
	}
	if flags != nil {
		for key, name := range map[string]string{"dns.address": "dns-listen", "web.address": "web-listen"} {
			flag := flags.Lookup(name)
			if flag == nil {
				return Config{}, fmt.Errorf("missing command flag %q", name)
			}
			if err := v.BindPFlag(key, flag); err != nil {
				return Config{}, err
			}
		}
	}
	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("read configuration %q: %w", path, err)
		}
	}
	var cfg Config
	if err := v.UnmarshalExact(&cfg, viper.DecodeHook(decodeValue), func(d *mapstructure.DecoderConfig) { d.WeaklyTypedInput = false }); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("configuration: %w", err)
	}
	return cfg, nil
}

// Configuration files must specify duration units. Environment variables are
// strings, so integers and comma-separated lists need explicit decoding too.
func decodeValue(from, to reflect.Type, value any) (any, error) {
	if from == nil || to == nil {
		return value, nil
	}
	if to == reflect.TypeFor[time.Duration]() {
		switch value := value.(type) {
		case time.Duration:
			return value, nil
		case string:
			return time.ParseDuration(value)
		default:
			return nil, fmt.Errorf("duration must include units, for example 10s or 1m")
		}
	}
	if to == reflect.TypeFor[int]() {
		if text, ok := value.(string); ok {
			return strconv.Atoi(text)
		}
		// YAML, JSON and TOML use different numeric Go types. Parse the decimal
		// value with the platform's int bounds rather than silently truncating.
		v := reflect.ValueOf(value)
		switch from.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return strconv.Atoi(strconv.FormatInt(v.Int(), 10))
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return strconv.Atoi(strconv.FormatUint(v.Uint(), 10))
		case reflect.Float32, reflect.Float64:
			return strconv.Atoi(strconv.FormatFloat(v.Float(), 'f', -1, 64))
		default:
			return nil, fmt.Errorf("value must be an integer")
		}
	}
	if to == reflect.TypeFor[[]string]() && from == reflect.TypeFor[string]() {
		text := value.(string)
		if text == "" {
			return []string{}, nil
		}
		parts := strings.Split(text, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts, nil
	}
	return value, nil
}
