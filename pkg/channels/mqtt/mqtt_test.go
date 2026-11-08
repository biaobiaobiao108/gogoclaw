package mqtt

import (
	"crypto/tls"
	"os"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestMQTTTLSConfigUsesSystemVerificationByDefault(t *testing.T) {
	channel := &MQTTChannel{cfg: &config.MQTTSettings{
		Broker: "mqtts://broker.example.com:8883",
	}}

	tlsConfig, err := channel.tlsConfig()
	if err != nil {
		t.Fatalf("tlsConfig() error = %v", err)
	}
	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %d, want TLS 1.2", tlsConfig.MinVersion)
	}
	if tlsConfig.InsecureSkipVerify {
		t.Fatal("certificate verification should be enabled by default")
	}
	if tlsConfig.ServerName != "broker.example.com" {
		t.Fatalf("ServerName = %q, want broker.example.com", tlsConfig.ServerName)
	}
	if tlsConfig.RootCAs != nil {
		t.Fatal("RootCAs should use the system pool when no CA file is configured")
	}
}

func TestMQTTTLSConfigAllowsExplicitInsecureDevelopmentMode(t *testing.T) {
	channel := &MQTTChannel{cfg: &config.MQTTSettings{
		Broker:                "mqtts://127.0.0.1:8883",
		TLSInsecureSkipVerify: true,
	}}

	tlsConfig, err := channel.tlsConfig()
	if err != nil {
		t.Fatalf("tlsConfig() error = %v", err)
	}
	if !tlsConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify should be enabled only by explicit configuration")
	}
}

func TestMQTTTLSConfigRejectsInvalidCAFile(t *testing.T) {
	caPath := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(caPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	channel := &MQTTChannel{cfg: &config.MQTTSettings{
		Broker:    "mqtts://broker.example.com:8883",
		TLSCAFile: caPath,
	}}

	if _, err := channel.tlsConfig(); err == nil || !strings.Contains(err.Error(), "no valid certificates") {
		t.Fatalf("tlsConfig() error = %v, want invalid CA error", err)
	}
}
