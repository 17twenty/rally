# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project: rally

This project is managed by galph, an autonomous coding loop driver.

## CRITICAL: Workspace Root

The current working directory IS the project root. DO NOT create a subdirectory named "rally".
All files (go.mod, cmd/, internal/, etc.) belong directly in this directory.

## Build & Test

To verify changes:
```bash
# Add your build command here (update .galphrc test_command too)
# Example: npm test, go test ./..., make test
```

## Conventions

- Make minimal, focused changes per task
- Run build/test verification before considering work complete
- Commit after each successful change
