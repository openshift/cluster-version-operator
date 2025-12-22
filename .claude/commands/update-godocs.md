---
name: update-godocs
description: Update comments on functions, structures, and properties within a Go file.
parameters:
  - name: path
    description: Path to the Go file to update.  One path at a time to make it easy to spread out review load.
    required: true
---

You are helping users update comments on functions, structures, and properties in Go files to make it easier for new users to understand package behavior.

## Context


## Your Task

Based on the user's path: "{{path}}"

1. Read the Go code for that path and other Go files (with the `.go` suffix, excluding the `_test.go` suffix) in that directory to understand the package's functionality.
2. Ensure that there is a Go file in the directory with the package-summary `// Package ...` comment.
    If there is not yet such a file, create one, including a comment that summarizes the overall package functionality.
3. For {{path}}, update the Go comments describing API elements to improve accuracy and clarity.
    Prioritize public functions, structures, and properties.
    If all public APIs already have accurate, clear comments, update comments on the file's internal APIs.
