import { createServer, type Server } from "node:http";

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { afterEach, describe, expect, it } from "vitest";

import { createApp } from "./server.js";

const servers: Server[] = [];

afterEach(async () => {
  await Promise.all(servers.splice(0).map((server) => new Promise<void>((resolve) => server.close(() => resolve()))));
});

describe("Anna dashboard MCP server", () => {
  it("enforces bearer authentication and exposes tools plus the widget resource", async () => {
    const annaToken = "a".repeat(32);
    const mcpToken = "m".repeat(32);
    const annaServer = createServer((request, response) => {
      expect(request.headers.authorization).toBe(`Bearer ${annaToken}`);
      response.setHeader("Content-Type", "application/json");
      response.end(JSON.stringify({ data: [], meta: { count: 0 } }));
    });
    servers.push(annaServer);
    const annaUrl = await listen(annaServer);

    const app = await createApp({
      port: 0,
      annaApiBaseUrl: annaUrl,
      annaApiToken: annaToken,
      mcpAuthToken: mcpToken,
      requestTimeoutMs: 2_000,
    });
    const appServer = app.listen(0, "127.0.0.1");
    servers.push(appServer);
    const appUrl = await listeningUrl(appServer);

    const unauthorized = await fetch(`${appUrl}/mcp`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} }),
    });
    expect(unauthorized.status).toBe(401);

    const client = new Client({ name: "anna-dashboard-test", version: "1.0.0" });
    const transport = new StreamableHTTPClientTransport(new URL(`${appUrl}/mcp`), {
      requestInit: { headers: { Authorization: `Bearer ${mcpToken}` } },
    });
    await client.connect(transport);

    const tools = await client.listTools();
    expect(tools.tools.map(({ name }) => name)).toEqual(expect.arrayContaining([
      "get_transactions",
      "get_expense_summary",
      "get_energy_trends",
      "get_fitness_trends",
      "render_anna_dashboard",
    ]));
    const renderTool = tools.tools.find(({ name }) => name === "render_anna_dashboard");
    expect(renderTool?._meta?.ui).toEqual(expect.objectContaining({ resourceUri: "ui://anna-dashboard/v1.html" }));

    const fitness = await client.callTool({ name: "get_fitness_trends", arguments: {} });
    expect(fitness.isError).not.toBe(true);
    expect(fitness.structuredContent).toEqual({
      fitness: { count: 0, availableSeries: [], points: [] },
    });

    const resource = await client.readResource({ uri: "ui://anna-dashboard/v1.html" });
    expect(resource.contents[0]?.mimeType).toBe("text/html;profile=mcp-app");
    expect(resource.contents[0]?.text).toContain("Anna Dashboard");

    await client.close();
  });
});

async function listen(server: Server): Promise<string> {
  server.listen(0, "127.0.0.1");
  return listeningUrl(server);
}

async function listeningUrl(server: Server): Promise<string> {
  if (!server.listening) await new Promise<void>((resolve) => server.once("listening", resolve));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("Expected a TCP test server");
  return `http://127.0.0.1:${address.port}`;
}
