// Package apicontract holds Go types generated from the OpenAPI contract in
// contract/*.openapi.yaml — the single source of truth shared with the Control
// Plane / bot (moenet-core). The hand-written structs in internal/{task,api,
// community,probe} are checked against these in contract_test.go, so a wire
// mismatch (e.g. dn42As string-vs-number) fails the build instead of the fleet.
//
// Do not edit the *.gen.go files by hand — regenerate with:
//
//	go generate ./internal/apicontract/
//
// The two specs share an `agentToken` security scheme, so they generate into
// separate subpackages (cpapi, agentapi) to avoid a duplicate-symbol clash.
//
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest -generate types -package cpapi -o cpapi/cp_agent_api.gen.go ../../contract/cp-agent-api.openapi.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest -generate types -package agentapi -o agentapi/agent_api.gen.go ../../contract/agent-api.openapi.yaml
package apicontract
