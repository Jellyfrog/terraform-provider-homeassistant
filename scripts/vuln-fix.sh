#!/usr/bin/env bash
# Bump every module that fails a security gate to the lowest version that fixes it (ADR-0027).
#
#   scripts/vuln-fix.sh <trivy.json> <govulncheck.json> <out-dir>
#
# Reads the findings the gates in .github/workflows/security.yml fail on:
#   - Trivy (`--format json`): CRITICAL or HIGH with a fixed version;
#   - govulncheck (`-format json`): vulnerabilities the provider's code calls (a finding whose
#     trace names a function), with a fixed version.
# For each module it runs `go get <module>@<lowest fixed version above the current one>`, the
# highest such version if several findings name it; for the standard library, `go get
# toolchain@go<version>`. Then `go mod tidy`. Nothing else is updated, so an indirect dependency
# moves only when a gate forces it.
# Writes <out-dir>/title and <out-dir>/body for the commit and the pull request, and nothing if no
# finding needs a bump. Unreachable or lower-severity findings are left alone: they fail no gate.
set -euo pipefail

trivy_json="${1:?usage: vuln-fix.sh <trivy.json> <govulncheck.json> <out-dir>}"
govuln_json="${2:?usage: vuln-fix.sh <trivy.json> <govulncheck.json> <out-dir>}"
out="${3:?usage: vuln-fix.sh <trivy.json> <govulncheck.json> <out-dir>}"
mkdir -p "$out"
rm -f "$out/title" "$out/body"

# Findings as "<module>\t<fixed versions, comma-separated>\t<advisory>", versions without "v".
findings="$(
  {
    jq -r '.Results[]? | .Vulnerabilities[]?
      | select(.Severity == "CRITICAL" or .Severity == "HIGH")
      | select((.FixedVersion // "") != "")
      | [.PkgName, .FixedVersion, .VulnerabilityID] | @tsv' "$trivy_json"
    jq -r 'select(.finding) | .finding | select(.trace[0].function != null)
      | select((.fixed_version // "") != "")
      | [.trace[0].module, .fixed_version, .osv] | @tsv' "$govuln_json"
  } | sed -E 's/\tv?([0-9])/\t\1/; s/, v/, /g; s/\tgo([0-9])/\t\1/'
)"
[[ -n "$findings" ]] || exit 0

# current <module>: the version in use, without "v" (stdlib: the toolchain, else the go line).
current() {
  if [[ "$1" == stdlib ]]; then
    go mod edit -json | jq -r '(.Toolchain // ("go" + .Go)) | ltrimstr("go")'
  else
    go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' "$1" | sed 's/^v//'
  fi
}

# newer <a> <b>: whether version a is above b.
newer() { [[ "$1" != "$2" && "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -1)" == "$1" ]]; }

declare -A target=() advisories=() from=()
while IFS=$'\t' read -r module fixed advisory; do
  [[ -v "from[$module]" ]] || from[$module]="$(current "$module")"
  # The lowest fixed version above the current one: a fix may exist on several release branches.
  need=""
  while read -r v; do
    if newer "$v" "${from[$module]}"; then need="$v"; break; fi
  done < <(tr ',' '\n' <<<"$fixed" | tr -d ' ' | sort -V)
  [[ -n "$need" ]] || continue # already fixed
  if [[ ! -v "target[$module]" ]] || newer "$need" "${target[$module]}"; then
    target[$module]="$need"
  fi
  case " ${advisories[$module]:-} " in
    *" $advisory "*) ;;
    *) advisories[$module]="${advisories[$module]:+${advisories[$module]} }$advisory" ;;
  esac
done <<<"$findings"
[[ ${#target[@]} -gt 0 ]] || exit 0

modules="$(printf '%s\n' "${!target[@]}" | sort)"
before="$(go list -m -f '{{.Path}} {{.Version}}' all)"
for module in $modules; do
  if [[ "$module" == stdlib ]]; then
    go get "toolchain@go${target[$module]}"
  else
    go get "$module@v${target[$module]}"
  fi
done
go mod tidy
# Modules Go raised too, because a bumped one requires their newer versions.
along="$(comm -13 <(sort <<<"$before") <(go list -m -f '{{.Path}} {{.Version}}' all | sort) \
  | grep -vxF -f <(for m in $modules; do echo "$m v${target[$m]}"; done) || true)"

name() { if [[ "$1" == stdlib ]]; then echo "the Go toolchain"; else echo "$1"; fi; }
count="$(wc -l <<<"$modules")"
if [[ "$count" -eq 1 ]]; then
  m="$modules"
  echo "chore(deps): bump $(name "$m") from ${from[$m]} to ${target[$m]} (security)" >"$out/title"
else
  echo "chore(deps): bump $count modules for security fixes" >"$out/title"
fi
{
  echo "Bumps what the security gates (Trivy, govulncheck) fail on to the lowest fixed version"
  echo "(ADR-0027). Nothing else is updated."
  echo
  echo "| Module | From | To | Advisories |"
  echo "|---|---|---|---|"
  for m in $modules; do
    echo "| $(name "$m") | ${from[$m]} | ${target[$m]} | ${advisories[$m]} |"
  done
  if [[ -n "$along" ]]; then
    echo
    echo "Raised along with them, because the bumped modules require these versions:"
    echo
    sed 's/^/- /' <<<"$along"
  fi
} >"$out/body"
