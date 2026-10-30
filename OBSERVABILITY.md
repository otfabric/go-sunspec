# Observability

This package defines no logger and no metrics: logging, metrics, retries and timeouts belong to the injected `*modbus.Client` ([API.md](API.md#observability-inherited)).

What it reports itself:

| What | Where |
|---|---|
| Something in a model that could not be decoded fully | `DecodedModel.Warnings` |
| A model without a schema, returned as raw registers | a warning from `ReadModel` |
| What detection tried | `ProbeAttempt` entries in `DetectionResult` |
| Why a decode or encode failed | `DecodeError`, `EncodeError` ([ERRORS.md](ERRORS.md)) |
| Accepted client writes on a `Server` | `ServerOptions.OnWrite` |
