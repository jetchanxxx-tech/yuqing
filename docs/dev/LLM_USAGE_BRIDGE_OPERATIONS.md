# LLM usage bridge configuration

K4 source development configuration; this document is not deployment evidence.

The Go HTTP service accepts `/internal/v1/billing/llm-authorizations` and `/internal/v1/billing/usage-events` only from a loopback socket and with `X-Billing-Service-Token`. The second endpoint also requires the accepted call's `X-LLM-Call-Permit`. Browser JWTs and tenant API keys cannot write this ledger.

Set `YUQING_BILLING_SERVICE_TOKEN` to the same privately injected service secret in Go and both Python engines. Go passes it to the insight/report engine via `X-Internal-Token`. Do not put it in source or logs. Set `YUQING_BILLING_URL` in Python to the Go service's loopback URL. Provider execution fails closed without accepted run identity and bridge configuration.

Set `YUQING_USAGE_OUTBOX_DIR` in both engines to a persistent volume directory, protected for the service account. Each attempt writes a private temporary JSON file, fsyncs it, atomically renames it and fsyncs the directory before provider execution. The completed usage record replaces the initial unknown intent before business JSON parsing. Only a confirmed committed Go ACK removes the file. Both engine application lifecycles start a drainer at startup and retry every 15 seconds. An exited process's unfinished intent (checked by boot ID and process start time, so PID reuse after restart is safe) is delivered as unknown usage; a live process's active attempt remains pending. Replays retain their event ID and call permit.

Do not remove this volume on engine restart, report failure or cancellation. Stop the corresponding engine before moving live pending intents to another host. Pending files or accepted authorizations without a reported usage event require reconciliation, not a fabricated zero-cost entry.

Provider prices use the existing `llm.models[].inputCostPerM` and `outputCostPerM` fields only when operations explicitly supplies `YUQING_PROVIDER_PRICE_VERSION` and `YUQING_PROVIDER_PRICE_CURRENCY=CNY` for verified CNY prices. Existing examples may describe USD; those are not silently treated as CNY. Missing/unverified currency, model or price leaves cost pending/null. The sandbox4/16CNY rates exist only in test fixtures. Report-credit purchases remain the user charging model, with zero additional token cash charge.

Deployment is not performed by this task. Production installation still requires a successful exact prod source run, verified prebuilt artifacts and coordinated server/worker/engine version/configuration changes.
