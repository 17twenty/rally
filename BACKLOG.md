# Backlog

Refinements, bug fixes, and post-PRD work items.

- [x] Bug when starting server on port 8432 - looks confused with Air reload - we need the .env and Taskfile.yml to be brought in sync and pushed through to the rally server: (refine-002, iteration 45)
```
...
(✓) Watching files
(✓) Watched file updated [ file=/Users/nickglynn/Projects/claude-code/rally/utils/templui.go ]
(✓) Post-generation event received, processing... [ needsRestart=true needsBrowserReload=true ]
(✓) Executing command [ command=go run ./cmd/server ]
(✓) Proxying [ from=http://127.0.0.1:7331 to=http://localhost:8432 ]
(✗) Proxy failed [ error=listen tcp 127.0.0.1:7331: bind: address already in use ]
Need to install the following packages:
@tailwindcss/cli@4.2.2
Ok to proceed? (y) 2026/04/03 17:08:06 WARN DATABASE_URL not set — running without database
2026/04/03 17:08:06 WARN VAULT_ENCRYPTION_KEY not set — credential vault disabled (set to a base64-encoded 32-byte key)
2026/04/03 17:08:06 INFO starting server addr=:8080
2026/04/03 17:08:06 ListenAndServe: listen tcp :8080: bind: address already in use
exit status 1
```
lets verify our app starts runs and then lets kill it and clean up the ports.
- [x] We shouldnt have SDR specific endpoints in rally - they have a workspace, tools and that's it. The /sdr endpoints and similar are bizarre - we likely need the ability for agents to persist, but they should use the workspace and plain files CSV/markdown etc for that (refine-001, iteration 37)
- [x] Make sure we dont run our server in Docker - verify Taskfile and usage. We only manage database containers and migrate them (refine-003, iteration 46)
- [x] Verify that Slack is the primary interface - we want to be able to onboard Rally via web but our AEs should be autonomous  - though tools, workspace and functionality can be exposed securely to them to run on Rallys main service - we shouldnt need to touch the webUI unless things need changing or updating (i.e. hiring a new AE and onboarding them, forcibly updating their soul/memory etc, and Slack comms should just reflect these changes - we would only onboard human employees by other means and our AEs should greet them when they join Slack (use an LLM call/their personlity, dont hardcode anything) and that's how we track our employees). (refine-004, iteration 47)
- [x] Cant start rally: (refine-005, iteration 48)
```bash
...
2026/04/03 17:50:46 INFO starting server addr=:8432
≈ tailwindcss v4.2.2

Error: Can't resolve 'tailwindcss' in '/Users/nickglynn/Projects/claude-code/rally/static'
Stopping...
(✓) Complete [ updates=73 duration=748.3025ms ]
task: Failed to run task "tailwind": exit status 1
task: Failed to run task "dev": exit status 201
```
- [x] Runtime usage error when we have our .env without anthropic/gpt, trying to chat to Rally results in: (refine-006, iteration 49)
```
(LLM unavailable: anthropic API error 401: {"type":"error","error":{"type":"authentication_error","message":"x-api-key header is required"},"request_id":"req_011CZhgsGsd4YiiXXPMr4xYY"})
```
- [x] New bug says """(LLM unavailable: openai API error 404: 404 page not found)""" - when we have greenthread creds when trying to talk to Rally via /chat (refine-008, iteration 52)
