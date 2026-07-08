#!/usr/bin/env bash
# Sync the API contract from moenet-core (the source of truth) and regenerate the
# Go types. Run whenever the contract changes upstream, then review `git diff`
# and commit. The Contract Sync CI job fails if this hasn't been run.
#
# Usage: scripts/sync-contract.sh [core-ref]   (default ref: dev)
set -euo pipefail

REF="${1:-dev}"
BASE="https://raw.githubusercontent.com/heichaowo/moenet-core/${REF}/contract"
cd "$(dirname "$0")/.."

for f in agent-api.openapi.yaml cp-agent-api.openapi.yaml; do
	echo "→ fetching contract/${f} from moenet-core@${REF}"
	curl -fsSL "${BASE}/${f}" -o "contract/${f}"
done

echo "→ regenerating Go types (go generate)"
go generate ./internal/apicontract/

echo "done — review 'git diff' and commit."
