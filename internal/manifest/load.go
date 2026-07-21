package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func DecodeStrict[T any](data []byte) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return value, fmt.Errorf("decode JSON: multiple top-level values")
		}
		return value, fmt.Errorf("decode trailing JSON: %w", err)
	}
	return value, nil
}

func LoadProfile(path string) (Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, fmt.Errorf("read profile %q: %w", path, err)
	}
	profile, err := DecodeStrict[Profile](data)
	if err != nil {
		return Profile{}, fmt.Errorf("profile %q: %w", path, err)
	}
	ApplyProfileDefaults(&profile)
	if err := ValidateProfile(profile); err != nil {
		return Profile{}, fmt.Errorf("profile %q: %w", path, err)
	}
	return profile, nil
}

func LoadEnvironment(path string) (Environment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Environment{}, fmt.Errorf("read environment %q: %w", path, err)
	}
	environment, err := DecodeStrict[Environment](data)
	if err != nil {
		return Environment{}, fmt.Errorf("environment %q: %w", path, err)
	}
	if err := ValidateEnvironment(environment); err != nil {
		return Environment{}, fmt.Errorf("environment %q: %w", path, err)
	}
	return environment, nil
}

func LoadDefaults(path string) (Defaults, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			defaults := BuiltinDefaults()
			return defaults, nil
		}
		return Defaults{}, fmt.Errorf("read defaults %q: %w", path, err)
	}
	defaults, err := DecodeStrict[Defaults](data)
	if err != nil {
		return Defaults{}, fmt.Errorf("defaults %q: %w", path, err)
	}
	ApplyDefaultsDefaults(&defaults)
	if err := ValidateDefaults(defaults); err != nil {
		return Defaults{}, fmt.Errorf("defaults %q: %w", path, err)
	}
	return defaults, nil
}
