# tfquiet

[![CI](https://github.com/winebarrel/tfquiet/actions/workflows/ci.yml/badge.svg)](https://github.com/winebarrel/tfquiet/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/winebarrel/tfquiet/graph/badge.svg)](https://codecov.io/gh/winebarrel/tfquiet)
[![AI Generated](https://img.shields.io/badge/AI%20Generated-Claude-orange?logo=anthropic)](https://claude.ai/claude-code)

tfquiet trims noise out of `terraform plan` output so the diff that matters stays in focus.

By default it removes:

| What | Why it's noise |
| ---- | -------------- |
| Pure `moved {}` blocks (rename only, no diff) | No actual state change — just an address rename |
| Pure `import {}` blocks (imported state already matches config) | No diff to act on; the import itself was already requested |
| `removed {}` with `lifecycle { destroy = false }` (state-only forget) | Block + the trailing `Warning: Some objects will no longer be managed` paragraph |
| `Refreshing state...` / `Preparing import...` / `Acquiring state lock` lines | Per-resource status chatter, not diff |
| Trailing `Note: You didn't use the -out option...` footer | Boilerplate |

**Anything that represents a real resource change is always shown.** If a moved or imported block also carries an in-place update (`~`), a replacement (`-/+` / `+/-`), or a create/destroy marker, the block stays in the output. Destroy blocks are likewise always shown — this includes both resources removed from configuration and `removed {}` blocks with `destroy = true` (Terraform renders them identically in plan output).

**The `Plan: …` summary line is never rewritten.** Counts always match what `terraform apply` will do.

## Installation

```
brew install winebarrel/tfquiet/tfquiet
```

## Usage

```
Usage: tfquiet [<file>] [flags]

Arguments:
  [<file>]    Terraform plan output file. If not specified, read from stdin.

Flags:
  -h, --help            Show help.
      --show-moved      Show moved blocks.
      --show-import     Show import blocks.
      --show-removed    Show removed{} lifecycle.destroy=false (state-only forget) blocks.
      --show-noise      Show refresh/lock lines and the trailing Note footer.
      --version
```

Pipe `terraform plan` straight through:

```sh
terraform plan | tfquiet
```

Recent Terraform releases keep ANSI color on even when stdout is a pipe; tfquiet preserves those sequences in the output. Pass `terraform plan -no-color` if you want plain text.

### Example

Given this `terraform plan` output:

```
terraform_data.imported: Preparing import... [id=import-stub-id]
terraform_data.to_be_destroyed: Refreshing state... [id=6ef59e95-...]
terraform_data.keep_one: Refreshing state... [id=49f3d73d-...]

Terraform used the selected providers to generate the following execution
plan. Resource actions are indicated with the following symbols:
  ~ update in-place
  - destroy
-/+ destroy and then create replacement

Terraform will perform the following actions:

  # terraform_data.imported will be updated in-place
  # (imported from "import-stub-id")
  ~ resource "terraform_data" "imported" {
        id     = "import-stub-id"
      + input  = "imported"
      + output = (known after apply)
    }

  # terraform_data.keep_one will be updated in-place
  ~ resource "terraform_data" "keep_one" {
      ~ input  = "keep-one" -> "keep-one-updated"
      ~ output = "keep-one" -> (known after apply)
    }

  # terraform_data.to_be_destroyed will be destroyed
  # (because terraform_data.to_be_destroyed is not in configuration)
  - resource "terraform_data" "to_be_destroyed" {
      - id     = "6ef59e95-..." -> null
      - input  = "destroy-me" -> null
    }

  # terraform_data.to_be_moved_old_name has moved to terraform_data.to_be_moved_new_name
    resource "terraform_data" "to_be_moved_new_name" {
        id     = "0ab5fef7-..."
    }

Plan: 1 to import, 0 to add, 2 to change, 1 to destroy.

─────────────────────────────────────────────────────────────────────────────

Note: You didn't use the -out option to save this plan...
```

`tfquiet` produces:

```
Terraform used the selected providers to generate the following execution
plan. Resource actions are indicated with the following symbols:
  ~ update in-place
  - destroy
-/+ destroy and then create replacement

Terraform will perform the following actions:

  # terraform_data.imported will be updated in-place
  # (imported from "import-stub-id")
  ~ resource "terraform_data" "imported" {
        id     = "import-stub-id"
      + input  = "imported"
      + output = (known after apply)
    }

  # terraform_data.keep_one will be updated in-place
  ~ resource "terraform_data" "keep_one" {
      ~ input  = "keep-one" -> "keep-one-updated"
      ~ output = "keep-one" -> (known after apply)
    }

  # terraform_data.to_be_destroyed will be destroyed
  # (because terraform_data.to_be_destroyed is not in configuration)
  - resource "terraform_data" "to_be_destroyed" {
      - id     = "6ef59e95-..." -> null
      - input  = "destroy-me" -> null
    }

Plan: 1 to import, 0 to add, 2 to change, 1 to destroy.
```

The `imported` block stays in the output because it carries an in-place update — even though it was added via `import {}`, dropping it would hide a real change. Only the pure `moved {}` rename and the refresh/preparation/note chatter are filtered out.
