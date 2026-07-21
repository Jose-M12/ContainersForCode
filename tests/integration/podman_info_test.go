package integration_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

type podmanInfoDocument struct {
	Host struct {
		CgroupVersion string `json:"cgroupVersion"`
		Security      struct {
			Rootless bool `json:"rootless"`
		} `json:"security"`
	} `json:"host"`
}

func decodePodmanInfo(data []byte) (bool, string, error) {
	var document podmanInfoDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return false, "", fmt.Errorf("decode Podman info JSON: %w", err)
	}
	if document.Host.CgroupVersion == "" {
		return false, "", fmt.Errorf("Podman info JSON omitted host.cgroupVersion")
	}
	return document.Host.Security.Rootless, document.Host.CgroupVersion, nil
}

func TestDecodePodmanInfo(t *testing.T) {
	rootless, cgroupVersion, err := decodePodmanInfo([]byte(`{
		"host": {
			"cgroupVersion": "v2",
			"security": {"rootless": true}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !rootless || cgroupVersion != "v2" {
		t.Fatalf("got rootless=%t cgroupVersion=%q", rootless, cgroupVersion)
	}
}

func TestDecodePodmanInfoRejectsMissingCgroupVersion(t *testing.T) {
	if _, _, err := decodePodmanInfo([]byte(`{"host":{"security":{"rootless":true}}}`)); err == nil {
		t.Fatal("expected missing host.cgroupVersion to fail closed")
	}
}
