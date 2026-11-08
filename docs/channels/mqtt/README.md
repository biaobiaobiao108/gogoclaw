# 📡 MQTT Channel

GoGoClaw supports any MQTT client as a chat channel. Devices or services publish requests to a broker; GoGoClaw subscribes, processes them, and publishes responses back.

## 🚀 Quick Start

**1. Add the channel to `~/.gogoclaw/config.json`:**

```json
{
  "channel_list": {
    "mqtt": {
      "enabled": true,
      "type": "mqtt",
      "settings": {
        "broker": "tcp://localhost:1883",
        "agent_id": "assistant"
      }
    }
  }
}
```

**2. Start the gateway:**

```bash
gogoclaw gateway
```

**3. Send a message from any MQTT client:**

```bash
mosquitto_pub -t "/gogoclaw/assistant/device1/request" \
  -m '{"text": "What is the CPU usage?"}'
```

**4. Subscribe to receive the response:**

```bash
mosquitto_sub -t "/gogoclaw/assistant/device1/response"
```

---

## 📨 Topic Structure

```
{prefix}/{agent_id}/{client_id}/request    # Client → GoGoClaw
{prefix}/{agent_id}/{client_id}/response   # GoGoClaw → Client
```

| Segment | Description |
|---------|-------------|
| `prefix` | Topic prefix, configured server-side. Default: `/gogoclaw` |
| `agent_id` | GoGoClaw instance identifier, set in `agent_id` config field |
| `client_id` | Client-defined session identifier — use a stable ID per device to maintain conversation context |

### Message Payload (JSON)

```json
{ "text": "your message here" }
```

---

## ⚙️ Configuration

### config.json

```json
{
  "channel_list": {
    "mqtt": {
      "enabled": true,
      "type": "mqtt",
      "settings": {
        "broker": "ssl://your-broker:8883",
        "agent_id": "assistant",
        "topic_prefix": "/gogoclaw",
        "client_id": "",
        "keep_alive": 60,
        "qos": 0
      }
    }
  }
}
```

### .security.yml (credentials)

Username and password are stored in `~/.gogoclaw/.security.yml`, not in `config.json`:

```yaml
channel_list:
  mqtt:
    settings:
      username: your_username
      password: your_password
```

### Configuration Fields

| Field | Location | Required | Default | Description |
|-------|----------|----------|---------|-------------|
| `broker` | `settings` | Yes | — | MQTT broker URL, e.g. `tcp://host:1883`, `ssl://host:8883` |
| `agent_id` | `settings` | Yes | — | Agent identifier, used as part of the topic path |
| `topic_prefix` | `settings` | No | `/gogoclaw` | Topic namespace prefix |
| `username` | `.security.yml` | No | — | Broker authentication username |
| `password` | `.security.yml` | No | — | Broker authentication password |
| `client_id` | `settings` | No | auto-generated | Paho client ID sent to the broker. Auto-generated as `gogoclaw-mqtt-{agent_id}-{8-char hex}` if not set; stays fixed for the process lifetime so reconnects reuse the same ID |
| `keep_alive` | `settings` | No | `60` | MQTT keepalive interval in seconds |
| `qos` | `settings` | No | `0` | QoS level for publish and subscribe: `0`, `1`, or `2` |
| `tls_ca_file` | `settings` | No | system roots | PEM CA bundle used to verify the broker certificate |
| `tls_insecure_skip_verify` | `settings` | No | `false` | Disable certificate verification. Use only for local development against a trusted test broker |

### Environment Variables

All fields can be set via environment variables:

| Variable | Field |
|----------|-------|
| `GOGOCLAW_CHANNELS_MQTT_BROKER` | `broker` |
| `GOGOCLAW_CHANNELS_MQTT_AGENT_ID` | `agent_id` |
| `GOGOCLAW_CHANNELS_MQTT_TOPIC_PREFIX` | `topic_prefix` |
| `GOGOCLAW_CHANNELS_MQTT_USERNAME` | `username` |
| `GOGOCLAW_CHANNELS_MQTT_PASSWORD` | `password` |
| `GOGOCLAW_CHANNELS_MQTT_CLIENT_ID` | `client_id` |
| `GOGOCLAW_CHANNELS_MQTT_KEEP_ALIVE` | `keep_alive` |
| `GOGOCLAW_CHANNELS_MQTT_QOS` | `qos` |
| `GOGOCLAW_CHANNELS_MQTT_TLS_CA_FILE` | `tls_ca_file` |
| `GOGOCLAW_CHANNELS_MQTT_TLS_INSECURE_SKIP_VERIFY` | `tls_insecure_skip_verify` |

---

## 🔄 Reconnection

GoGoClaw automatically reconnects to the broker if the connection is lost, with a 5-second retry interval. On reconnect, the subscription is re-established automatically. The broker-side client ID stays the same across reconnects so the broker correctly identifies it as the same session.

---

## ⚠️ Notes

- **TLS**: SSL/TLS is supported (`ssl://` broker URL). The system certificate pool verifies the broker by default. Set `tls_ca_file` for a private CA. `tls_insecure_skip_verify: true` is an explicit development-only opt-out.
- **Streaming**: Streaming responses send multiple messages to the response topic; concatenate them in order.
- **client_id vs session ID**: The `client_id` in the topic path is set by your client application and identifies the conversation session. It is separate from the broker-level client ID used by GoGoClaw's paho connection.
- **Multiple instances**: If you run multiple GoGoClaw instances against the same broker with the same `agent_id`, set distinct `client_id` values to avoid broker-level conflicts.
