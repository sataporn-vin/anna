import { describe, expect, it } from "vitest";

import { loadConfig } from "./config.js";

const validEnv = {
  ANNA_API_BASE_URL: "https://anna.example.com/",
  ANNA_API_TOKEN: "a".repeat(32),
  MCP_AUTH_TOKEN: "m".repeat(32),
};

describe("loadConfig", () => {
  it("loads defaults and removes trailing URL slashes", () => {
    expect(loadConfig(validEnv)).toEqual({
      port: 3000,
      annaApiBaseUrl: "https://anna.example.com",
      annaApiToken: "a".repeat(32),
      mcpAuthToken: "m".repeat(32),
      requestTimeoutMs: 10_000,
      widgetDomain: undefined,
    });
  });

  it("rejects missing or weak server credentials", () => {
    expect(() => loadConfig({ ...validEnv, MCP_AUTH_TOKEN: "short" })).toThrow(/at least 32/);
    expect(() => loadConfig({ ...validEnv, ANNA_API_TOKEN: undefined })).toThrow(/ANNA_API_TOKEN is required/);
  });
});
