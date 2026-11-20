# Photon iMessage protocol

These protocol files are copied from the MIT-licensed `@photon-ai/advanced-imessage@2.5.0` package, which publishes the Photon iMessage v1 API schema. The package source is:

```text
https://github.com/photon-hq/advanced-imessage-ts
```

The upstream license is included in `LICENSE`.

The upstream package exports the schema from `buf.build/photon-hq/imessage` and includes the Google API annotation files needed by the service definitions. Keep the source pin in this file when refreshing the schema so generated code can be reproduced.

Regenerate the Go clients from this directory with the pinned `buf`, `protoc-gen-go`, and `protoc-gen-go-grpc` tools:

```sh
buf generate
```

The generated package is `photonv1` under `../internal/photon/imessage/v1`. Generated files are committed because the gateway build does not require `buf` or `protoc` at runtime.
