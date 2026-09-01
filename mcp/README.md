# Anna dashboard MCP app

This directory contains a read-only MCP server and an inline React/Recharts dashboard for Anna's personal data. The server calls the existing Anna REST API; the browser widget calls only MCP tools and never receives the Anna API token or MongoDB credentials.

## Scope

The first release includes:

- expense totals grouped by top-level category and local date;
- filters for inclusive date range, account IDs, and category;
- recent matching transactions with expandable details;
- Kokomi energy totals, timeline, and strongest positive/negative experiences;
- weight, skeletal muscle mass (SMM), percent body fat (PBF), and total-body-water series when measurement records exist.

The tools are `get_transactions`, `get_expense_summary`, `get_energy_trends`, `get_fitness_trends`, `get_dashboard_data`, and `render_anna_dashboard`. `get_dashboard_data` is app-only; `render_anna_dashboard` owns the `ui://anna-dashboard/v1.html` resource.

Life-log and todo views, write tools, OAuth, and public ChatGPT app submission are outside this MVP.

## Local development

Node.js 22 or later is required. Copy the environment template and replace every placeholder:

```sh
cd /Users/sataporn.vin/bag/codex/anna/mcp
cp /Users/sataporn.vin/bag/codex/anna/mcp/.env.example /Users/sataporn.vin/bag/codex/anna/mcp/.env
```

`ANNA_API_BASE_URL` points to the existing Anna API. `ANNA_API_TOKEN` is that API's `AUTH_BEARER_TOKEN`. `MCP_AUTH_TOKEN` must be a different random value of at least 32 characters.

Install, build, and start the MCP server:

```sh
cd /Users/sataporn.vin/bag/codex/anna/mcp
npm ci
npm run build
set -a
source /Users/sataporn.vin/bag/codex/anna/mcp/.env
set +a
npm start
```

The MCP endpoint is `http://localhost:3000/mcp`; readiness is public at `http://localhost:3000/health/ready`. MCP requests require `Authorization: Bearer <MCP_AUTH_TOKEN>`.

To inspect the server, run the official MCP Inspector in another terminal and configure the Streamable HTTP URL plus the bearer header in its UI:

```sh
cd /Users/sataporn.vin/bag/codex/anna/mcp
npx @modelcontextprotocol/inspector@latest
```

Run the automated checks with:

```sh
cd /Users/sataporn.vin/bag/codex/anna/mcp
npm test
```

## Railway service

Deploy this as a separate Railway service from the Anna REST API. Keep the service root at the repository root and set the Railway config-file path to `/mcp/railway.json`; its Dockerfile path is `mcp/Dockerfile`.

Configure:

```text
ANNA_API_BASE_URL=https://<anna-api-domain>
ANNA_API_TOKEN=<anna-api-auth-bearer-token>
MCP_AUTH_TOKEN=<different-random-token-at-least-32-characters>
ANNA_REQUEST_TIMEOUT_MS=10000
WIDGET_DOMAIN=https://<dedicated-widget-origin>
```

Railway provides `PORT`. Generate an HTTPS domain and verify `/health/ready` before connecting a client. Do not configure `MONGODB_URI` on this service.

## Authentication limitation

The static MCP bearer token is appropriate only for private development or a single-user MCP client that can attach a fixed header. It is not sufficient for a public ChatGPT app. Before public distribution, replace it with OAuth 2.1 authorization, keep the Anna API token server-side, configure the final dedicated `WIDGET_DOMAIN`, and complete ChatGPT app submission review.
