# Current Hardware Support Status and Serial Tool Proposal

## Current Status Conclusion

The project's existing hardware-related capabilities mainly fall into two lines:

1. Device event monitoring
   - `pkg/devices` already implements a device event service.
   - The only current source is the Linux USB hot-plug event source `pkg/devices/sources/usb_linux.go`.
   - The capability is positioned as "discovery and notification", not "bus read/write control".

2. Hardware control Tools
   - `pkg/tools/hardware/i2c*.go`: I2C Tool, supporting `detect`, `scan`, `read`, and `write`.
   - `pkg/tools/hardware/spi*.go`: SPI Tool, supporting `list`, `transfer`, and `read`.
   - Both types of Tools are currently enabled only on Linux hosts and depend directly on `/dev/i2c-*` and `/dev/spidev*`.

Therefore, in terms of "hardware support capabilities", the project already has:

- Linux USB device plug/unplug awareness
- Linux I2C bus control
- Linux SPI bus control

But it still lacks:

- Serial/UART control
- Hardware control Tools that can be used directly on macOS / Windows
- A cross-bus capability model oriented toward a unified hardware abstraction

## What This Change Adds

This change adds a built-in `serial` Tool and integrates it into the existing Tool system:

- Configuration item: `tools.serial.enabled`
- Tool registration: `pkg/agent/agent_init.go`
- Web tools page: `/api/tools` can display and toggle `serial`
- Frontend status copy: adds `requires_serial_platform`

## Serial Tool Design

`serial` uses a stateless invocation model. Each request opens and closes the port itself, avoiding the need to maintain serial session state between agent turns.

Supported actions:

- `list`: enumerate host serial ports
- `read`: read a specified number of bytes from the serial port
- `write`: write bytes or text to the serial port

Common parameters:

- `port`
- `baud`
- `data_bits`
- `parity`
- `stop_bits`
- `timeout_ms`

Current baud rate implementation boundaries:

- Windows allows the range accepted at the Tool layer: `50-4000000`
- Linux / macOS currently support only standard termios baud rates, with actual support up to `230400`
- Therefore, for portable values of `baud` across platforms, common standard rates of `230400` and below should be preferred

Security constraints:

- `write` must explicitly pass `confirm: true`
- A single read or write payload is limited to `4096` bytes
- `port` accepts only allowlisted serial port names:
  - Linux / macOS allow only `/dev/tty*`, `/dev/cu.*`, and the corresponding shorthand device names
  - Windows allows only `COM\d+` or `\\.\COM\d+`
  - Explicitly reject non-serial-device paths such as `..`, ordinary absolute file paths, and drive-letter paths, to avoid path traversal or accidentally opening arbitrary files

## Cross-Platform Implementation Boundaries

- Linux / macOS:
  - Configure serial port parameters using `golang.org/x/sys/unix` and termios.
  - Currently only standard termios baud rate mappings are integrated, up to `230400`; higher rates such as `460800`, `921600`, `1000000`, and `2000000` have not yet been added.
  - Enumerate and access devices through `/dev/...`.

- Windows:
  - Configure `DCB` and `COMMTIMEOUTS` using the `kernel32` serial APIs.
  - Reads and writes currently still use synchronous `ReadFile` / `WriteFile`; once the syscall has entered execution, turn context cancellation cannot interrupt it immediately and can only wait for `COMMTIMEOUTS` to trigger before returning.
  - Enumerate ports through the registry key `HARDWARE\\DEVICEMAP\\SERIALCOMM`.

- Other platforms:
  - The `serial` Tool explicitly returns unsupported and does not silently degrade.

## Follow-Up Recommendations

1. If a persistent interactive serial session is needed, add a session-style Tool instead of having the LLM repeatedly poll with short-lived connections.
2. If CAN, GPIO, and PWM support is needed later, extract a unified hardware capability description layer instead of continuing to distinguish capabilities only by Tool name.
3. For production-grade stability, add real serial loopback tests, covering at least Linux PTY and Windows COM emulation scenarios.
