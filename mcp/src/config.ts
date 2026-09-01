export type Config = {
  port: number;
  annaApiBaseUrl: string;
  annaApiToken: string;
  mcpAuthToken: string;
  requestTimeoutMs: number;
  widgetDomain?: string;
};

function required(name: string, value: string | undefined): string {
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

function token(name: string, value: string | undefined): string {
  const result = required(name, value);
  if (result.length < 32) {
    throw new Error(`${name} must contain at least 32 characters`);
  }
  return result;
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
  const port = Number(env.PORT ?? 3000);
  const requestTimeoutMs = Number(env.ANNA_REQUEST_TIMEOUT_MS ?? 10_000);
  if (!Number.isInteger(port) || port < 1 || port > 65_535) {
    throw new Error("PORT must be an integer between 1 and 65535");
  }
  if (!Number.isInteger(requestTimeoutMs) || requestTimeoutMs < 100 || requestTimeoutMs > 60_000) {
    throw new Error("ANNA_REQUEST_TIMEOUT_MS must be between 100 and 60000");
  }

  return {
    port,
    annaApiBaseUrl: required("ANNA_API_BASE_URL", env.ANNA_API_BASE_URL).replace(/\/$/, ""),
    annaApiToken: token("ANNA_API_TOKEN", env.ANNA_API_TOKEN),
    mcpAuthToken: token("MCP_AUTH_TOKEN", env.MCP_AUTH_TOKEN),
    requestTimeoutMs,
    widgetDomain: env.WIDGET_DOMAIN?.replace(/\/$/, ""),
  };
}
