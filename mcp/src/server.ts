import { createHash, timingSafeEqual } from "node:crypto";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import {
  registerAppResource,
  registerAppTool,
  RESOURCE_MIME_TYPE,
} from "@modelcontextprotocol/ext-apps/server";
import { createMcpExpressApp } from "@modelcontextprotocol/sdk/server/express.js";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import type { NextFunction, Request, Response } from "express";
import { z } from "zod";

import { AnnaClient } from "./annaClient.js";
import { loadConfig, type Config } from "./config.js";
import { loadDashboard, summarizeEnergy } from "./dashboard.js";
import type { DashboardFilters } from "./types.js";

const WIDGET_URI = "ui://anna-dashboard/v1.html";

const filterShape = {
  occurredFrom: z.string().date().optional().describe("Inclusive local date in YYYY-MM-DD format."),
  occurredTo: z.string().date().optional().describe("Inclusive local date in YYYY-MM-DD format."),
  accountIds: z.array(z.string().min(1).max(100)).max(20).optional(),
  category: z.string().min(1).max(100).optional().describe("Top-level or nested category segment."),
};

const transactionSchema = z.object({
  id: z.string(),
  occurredOn: z.string(),
  occurredAt: z.string().optional(),
  amountMinor: z.number().int(),
  currency: z.string(),
  transactionKind: z.string(),
  accountId: z.string(),
  paymentChannelId: z.string().optional(),
  descriptor: z.string().optional(),
  merchantName: z.string().optional(),
  categoryPath: z.array(z.string()),
  note: z.string().optional(),
});

const expenseSummarySchema = z.object({
  count: z.number().int(),
  totalMinor: z.number().int(),
  currency: z.string(),
  byCategory: z.array(z.object({ category: z.string(), totalMinor: z.number().int(), count: z.number().int() })),
  byDay: z.array(z.object({ occurredOn: z.string(), totalMinor: z.number().int(), count: z.number().int() })),
});

const experienceSchema = z.object({
  id: z.string(),
  occurredOn: z.string(),
  summary: z.string(),
  energyDelta: z.number(),
  context: z.array(z.string()),
});

const energySchema = z.object({
  count: z.number().int(),
  totalDelta: z.number(),
  averageDelta: z.number(),
  daily: z.array(z.object({ occurredOn: z.string(), totalDelta: z.number(), count: z.number().int() })),
  topPositive: z.array(experienceSchema),
  topNegative: z.array(experienceSchema),
});

const fitnessPointSchema = z.object({
  id: z.string(),
  occurredOn: z.string(),
  weight: z.number().optional(),
  smm: z.number().optional(),
  pbf: z.number().optional(),
  totalBodyWater: z.number().optional(),
});

const fitnessSchema = z.object({
  count: z.number().int(),
  availableSeries: z.array(z.enum(["weight", "smm", "pbf", "totalBodyWater"])),
  points: z.array(fitnessPointSchema),
});

const dashboardSchema = z.object({
  filters: z.object(filterShape),
  expenses: expenseSummarySchema,
  transactions: z.array(transactionSchema),
  energy: energySchema,
  fitness: fitnessSchema,
});

export async function createApp(config: Config) {
  const app = createMcpExpressApp();
  const client = new AnnaClient(config.annaApiBaseUrl, config.annaApiToken, config.requestTimeoutMs);
  const widgetHtml = await readWidgetHtml();

  app.get("/health/ready", (_request, response) => {
    response.json({ status: "ok" });
  });

  app.use("/mcp", bearerAuth(config.mcpAuthToken));
  app.post("/mcp", async (request, response) => {
    const server = createMcpServer(client, widgetHtml, config.widgetDomain);
    try {
      const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined });
      await server.connect(transport);
      await transport.handleRequest(request, response, request.body);
      response.on("close", () => {
        void transport.close();
        void server.close();
      });
    } catch (error) {
      console.error("MCP request failed", error);
      if (!response.headersSent) {
        response.status(500).json({
          jsonrpc: "2.0",
          error: { code: -32603, message: "Internal server error" },
          id: null,
        });
      }
    }
  });
  app.get("/mcp", methodNotAllowed);
  app.delete("/mcp", methodNotAllowed);

  return app;
}

function createMcpServer(client: AnnaClient, widgetHtml: string, widgetDomain?: string): McpServer {
  const server = new McpServer(
    { name: "anna-dashboard", version: "0.1.0" },
    {
      instructions:
        "Use Anna's read-only tools for personal finance, Kokomi energy, and fitness trends. Call data tools before summarizing. Use render_anna_dashboard when visual comparison, filtering, charts, or drill-down would help. Never infer missing measurements or expose internal credentials.",
    },
  );

  server.registerTool(
    "get_transactions",
    {
      title: "Get transactions",
      description: "Retrieve expense transactions for a date range, account set, or category for review and drill-down.",
      inputSchema: {
        ...filterShape,
        limit: z.number().int().min(1).max(100).default(50),
      },
      outputSchema: { transactions: z.array(transactionSchema) },
      annotations: readOnlyAnnotations(),
    },
    async ({ limit, ...filters }) => {
      validateDateRange(filters);
      const transactions = await client.getTransactions(filters, limit);
      return {
        structuredContent: { transactions },
        content: [{ type: "text", text: `Found ${transactions.length} expense transactions.` }],
      };
    },
  );

  server.registerTool(
    "get_expense_summary",
    {
      title: "Get expense summary",
      description: "Summarize expense totals by category and day using server-side aggregation.",
      inputSchema: filterShape,
      outputSchema: { summary: expenseSummarySchema },
      annotations: readOnlyAnnotations(),
    },
    async (filters) => {
      validateDateRange(filters);
      const summary = await client.getExpenseSummary(filters);
      return {
        structuredContent: { summary },
        content: [
          {
            type: "text",
            text: `Summarized ${summary.count} expenses totaling ${formatMinor(summary.totalMinor, summary.currency)}.`,
          },
        ],
      };
    },
  );

  server.registerTool(
    "get_energy_trends",
    {
      title: "Get Kokomi energy trends",
      description: "Summarize reported energy changes and identify the strongest positive and negative experiences.",
      inputSchema: {
        occurredFrom: filterShape.occurredFrom,
        occurredTo: filterShape.occurredTo,
      },
      outputSchema: { energy: energySchema },
      annotations: readOnlyAnnotations(),
    },
    async (filters) => {
      validateDateRange(filters);
      const experiences = await client.getExperiences(filters);
      const energy = summarizeEnergy(experiences);
      return {
        structuredContent: { energy },
        content: [{ type: "text", text: `Summarized ${energy.count} energy experiences.` }],
      };
    },
  );

  server.registerTool(
    "get_fitness_trends",
    {
      title: "Get fitness and InBody trends",
      description: "Retrieve available weight, SMM, PBF, and total-body-water measurement series.",
      inputSchema: {
        occurredFrom: filterShape.occurredFrom,
        occurredTo: filterShape.occurredTo,
      },
      outputSchema: { fitness: fitnessSchema },
      annotations: readOnlyAnnotations(),
    },
    async (filters) => {
      validateDateRange(filters);
      const fitness = await client.getFitnessTrends(filters);
      return {
        structuredContent: { fitness },
        content: [{ type: "text", text: `Found ${fitness.count} fitness measurement points.` }],
      };
    },
  );

  server.registerTool(
    "get_dashboard_data",
    {
      title: "Refresh Anna dashboard data",
      description: "Load the read-only data needed by the Anna dashboard for the selected filters.",
      inputSchema: filterShape,
      outputSchema: { dashboard: dashboardSchema },
      annotations: readOnlyAnnotations(),
      _meta: { ui: { visibility: ["app"] } },
    },
    async (filters) => {
      validateDateRange(filters);
      const dashboard = await loadDashboard(client, filters);
      return {
        structuredContent: { dashboard },
        content: [{ type: "text", text: "Dashboard data refreshed." }],
      };
    },
  );

  registerAppTool(
    server,
    "render_anna_dashboard",
    {
      title: "Render Anna dashboard",
      description:
        "Render an interactive Anna dashboard. Use when the user asks to view, compare, filter, chart, or drill into expenses, energy, or fitness data.",
      inputSchema: filterShape,
      outputSchema: { dashboard: dashboardSchema },
      annotations: readOnlyAnnotations(),
      _meta: {
        ui: { resourceUri: WIDGET_URI },
        "openai/toolInvocation/invoking": "Loading Anna dashboard…",
        "openai/toolInvocation/invoked": "Anna dashboard ready.",
      },
    },
    async (filters) => {
      validateDateRange(filters);
      const dashboard = await loadDashboard(client, filters);
      return {
        structuredContent: { dashboard },
        content: [
          {
            type: "text",
            text: `Showing ${dashboard.expenses.count} expenses, ${dashboard.energy.count} energy entries, and ${dashboard.fitness.count} fitness measurements.`,
          },
        ],
      };
    },
  );

  const ui = {
    prefersBorder: true,
    csp: { connectDomains: [] as string[], resourceDomains: [] as string[] },
    ...(widgetDomain ? { domain: widgetDomain } : {}),
  };
  registerAppResource(
    server,
    "Anna dashboard",
    WIDGET_URI,
    { description: "Interactive expense, Kokomi energy, and fitness dashboard.", _meta: { ui } },
    async () => ({
      contents: [
        {
          uri: WIDGET_URI,
          mimeType: RESOURCE_MIME_TYPE,
          text: widgetHtml,
          _meta: {
            ui,
            "openai/widgetDescription":
              "Interactive Anna dashboard with expense breakdowns, daily trends, transaction drill-down, Kokomi energy, and fitness series.",
          },
        },
      ],
    }),
  );

  return server;
}

function readOnlyAnnotations() {
  return { readOnlyHint: true, destructiveHint: false, openWorldHint: false } as const;
}

function validateDateRange(filters: DashboardFilters): void {
  if (filters.occurredFrom && filters.occurredTo && filters.occurredFrom > filters.occurredTo) {
    throw new Error("occurredFrom must be on or before occurredTo");
  }
}

function formatMinor(minor: number, currency: string): string {
  return new Intl.NumberFormat("en-US", { style: "currency", currency }).format(minor / 100);
}

function bearerAuth(expectedToken: string) {
  const expected = createHash("sha256").update(expectedToken).digest();
  return (request: Request, response: Response, next: NextFunction) => {
    const authorization = request.header("authorization") ?? "";
    if (!authorization.startsWith("Bearer ")) {
      response.setHeader("WWW-Authenticate", 'Bearer realm="anna-dashboard"');
      response.status(401).json({ error: "A bearer token is required." });
      return;
    }
    const provided = createHash("sha256").update(authorization.slice("Bearer ".length)).digest();
    if (!timingSafeEqual(provided, expected)) {
      response.setHeader("WWW-Authenticate", 'Bearer realm="anna-dashboard"');
      response.status(401).json({ error: "The bearer token is invalid." });
      return;
    }
    next();
  };
}

function methodNotAllowed(_request: Request, response: Response): void {
  response.status(405).json({
    jsonrpc: "2.0",
    error: { code: -32000, message: "Method not allowed." },
    id: null,
  });
}

async function readWidgetHtml(): Promise<string> {
  const moduleDirectory = dirname(fileURLToPath(import.meta.url));
  const candidates = [resolve(moduleDirectory, "../widget/index.html"), resolve(process.cwd(), "dist/widget/index.html")];
  for (const candidate of candidates) {
    try {
      return await readFile(candidate, "utf8");
    } catch {
      // Try the next build location.
    }
  }
  throw new Error("Anna dashboard widget is missing; run npm run build:widget first.");
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const config = loadConfig();
  const app = await createApp(config);
  const listener = app.listen(config.port, () => {
    console.log(`Anna dashboard MCP server listening on port ${config.port}`);
  });
  listener.on("error", (error) => {
    console.error("Failed to start Anna dashboard MCP server", error);
    process.exit(1);
  });
}
