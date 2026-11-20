package config

import "testing"

func TestPhotonSettingsDecodeAndCredentials(t *testing.T) {
	channels := ChannelsConfig{
		"imessage": &Channel{
			Enabled: true,
			Type:    ChannelPhoton,
			Settings: RawNode(`{
				"project_id":"sp-test",
				"project_secret":"secret-value",
				"disable_read_receipts":true,
				"streaming":{"enabled":true,"throttle_seconds":5,"min_growth_chars":200}
			}`),
		},
	}
	if err := InitChannelList(channels); err != nil {
		t.Fatalf("InitChannelList() error = %v", err)
	}
	decoded, err := channels["imessage"].GetDecoded()
	if err != nil {
		t.Fatalf("GetDecoded() error = %v", err)
	}
	settings, ok := decoded.(*PhotonSettings)
	if !ok {
		t.Fatalf("settings type = %T, want *PhotonSettings", decoded)
	}
	if settings.ProjectID != "sp-test" || settings.ProjectSecret.String() != "secret-value" {
		t.Fatalf("photon credentials = %q/%q", settings.ProjectID, settings.ProjectSecret.String())
	}
	if !settings.DisableReadReceipts || !settings.Streaming.Enabled || settings.Streaming.ThrottleSeconds != 5 {
		t.Fatalf("photon settings not decoded: %+v", settings)
	}
}

func TestPhotonIsTrackedForPlatformTextDelivery(t *testing.T) {
	channels := ChannelsConfig{
		"imessage": &Channel{Type: ChannelPhoton},
	}
	if !channels.TracksPlatformTextDelivery("imessage") {
		t.Fatal("photon should use durable platform text delivery tracking")
	}
}

func TestDefaultConfigIncludesPhotonChannel(t *testing.T) {
	channels := DefaultConfig().Channels
	channel := channels.Get(ChannelPhoton)
	if channel == nil {
		t.Fatal("default config is missing photon channel")
	}
	if channel.Type != ChannelPhoton {
		t.Fatalf("photon type = %q, want %q", channel.Type, ChannelPhoton)
	}
}
