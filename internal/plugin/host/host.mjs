// Octopus plugin host.
//
// One Node process loads every enabled provider plugin from installed.json and
// exposes:
//   - a small control API over 127.0.0.1 for auth/models/status
//   - a per-route data plane that Octopus Relay treats as a normal upstream
//
// It is not a public service: the listener binds loopback and every request
// must carry the random Bearer token passed by the parent process.
import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { Readable } from "node:stream";
import { pathToFileURL } from "node:url";

const DATA_DIR = path.resolve(process.env.OCTOPUS_PLUGIN_DATA_DIR || "data/plugins");
const TOKEN = process.env.OCTOPUS_PLUGIN_TOKEN || crypto.randomBytes(24).toString("hex");
const INSTALLED_FILE = path.join(DATA_DIR, "installed.json");
const AUTH_FILE = path.join(DATA_DIR, "auth.json");
const LOG_LIMIT = 300;

const plugins = new Map(); // plugin id -> { entry, functions, hooks, error }
const providers = new Map(); // `${pluginID}|${providerID}` -> provider state
const sessions = new Map();
const logs = [];
let config = { provider: {} };
let authStore = readJSON(AUTH_FILE, {});

function logLine(level, message) {
  const line = `${new Date().toISOString()} ${level} ${message}`;
  logs.push(line);
  if (logs.length > LOG_LIMIT) logs.shift();
  console.error(`[plugin-host] ${line}`);
}

function readJSON(file, fallback) {
  try {
    let text = fs.readFileSync(file, "utf8");
    if (text.charCodeAt(0) === 0xfeff) text = text.slice(1);
    return JSON.parse(text);
  } catch {
    return fallback;
  }
}

function writeJSON(file, value, mode = 0o600) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const tmp = `${file}.tmp-${process.pid}`;
  fs.writeFileSync(tmp, `${JSON.stringify(value, null, 2)}\n`, { mode });
  try {
    fs.chmodSync(tmp, mode);
  } catch {}
  fs.renameSync(tmp, file);
}

function hash(value) {
  return crypto.createHash("sha256").update(String(value)).digest("hex").slice(0, 16);
}

function providerKey(pluginID, providerID) {
  return `${pluginID}|${providerID}`;
}

function getProvider(pluginID, providerID) {
  return providers.get(providerKey(pluginID, providerID));
}

function allProviders() {
  return [...providers.values()];
}

function authFor(providerID) {
  return authStore[providerID] ?? authStore[`${providerID}#0`] ?? null;
}

function authLabel(providerID) {
  const auth = authFor(providerID);
  if (!auth) return "";
  return auth.accountId || auth.name || auth.uid || auth.email || "Signed in";
}

function isLoggedIn(providerID) {
  const auth = authFor(providerID);
  if (!auth || (auth.type !== "oauth" && auth.type !== "api")) return false;
  return !!(auth.access || auth.key || auth.source === "desktop" || auth.refresh);
}

function saveAuth(providerID, value) {
  if (!value) {
    delete authStore[providerID];
  } else {
    authStore[providerID] = value;
  }
  writeJSON(AUTH_FILE, authStore, 0o600);
  for (const provider of providers.values()) {
    if (provider.providerID === providerID) {
      provider.loaderFetch = null;
      provider.modelCache = null;
    }
  }
}

function clearAuth(providerID) {
  for (const key of Object.keys(authStore)) {
    if (key === providerID || key.startsWith(`${providerID}#`)) delete authStore[key];
  }
  writeJSON(AUTH_FILE, authStore, 0o600);
  for (const provider of providers.values()) {
    if (provider.providerID === providerID) {
      provider.loaderFetch = null;
      provider.modelCache = null;
    }
  }
}

function setAuthFromResult(providerID, result) {
  if (!result) return;
  if (result.type === "failed") throw new Error(result.error || "sign-in failed");
  if (result.type === "success") {
    const saved = {
      type: "oauth",
      access: result.access || "",
      refresh: result.refresh || "",
      expires: result.expires || 0,
      accountId: result.accountId || result.name || result.uid || "Signed in",
      uid: result.uid || "",
      domain: result.domain || "",
      ...(result.enterpriseId ? { enterpriseId: result.enterpriseId } : {}),
      ...(result.source ? { source: result.source } : {}),
      ...(result.refreshExpiresAt ? { refreshExpiresAt: result.refreshExpiresAt } : {}),
      ...(result.tokenType ? { tokenType: result.tokenType } : {}),
    };
    saveAuth(providerID, saved);
    return;
  }
  // Some API-key methods return the auth object directly.
  if (result.type === "api" || result.key) {
    saveAuth(providerID, result);
    return;
  }
  throw new Error("plugin returned an unknown sign-in result");
}

function makeClient() {
  return {
    auth: {
      get: async (opts) => structuredClone(authFor(opts?.path?.id ?? opts?.id) ?? null),
      set: async (opts) => {
        const id = opts?.path?.id ?? opts?.id ?? opts?.providerID;
        const body = opts && Object.hasOwn(opts, "body") ? opts.body : opts?.auth;
        if (id && body !== undefined) saveAuth(id, body);
      },
    },
  };
}

function makeMagpie(entry) {
  return {
    option: (providerID, key) => entry.options?.[providerID]?.[key] ?? entry.options?.[key],
  };
}

async function importPluginFunctions(dir) {
  const pkg = readJSON(path.join(dir, "package.json"), null);
  if (!pkg) throw new Error("plugin has no readable package.json");
  const candidates = [];
  const addExport = (value) => {
    if (!value) return;
    if (typeof value === "string") candidates.push(value);
    else if (typeof value === "object") {
      if (typeof value.import === "string") candidates.push(value.import);
      if (typeof value.default === "string") candidates.push(value.default);
      if (typeof value.require === "string") candidates.push(value.require);
      if (value["."]) addExport(value["."]);
      if (value["./server"]) addExport(value["./server"]);
    }
  };
  if (pkg.exports) {
    if (pkg.exports["./server"]) addExport(pkg.exports["./server"]);
    else if (pkg.exports["."]) addExport(pkg.exports["."]);
    else addExport(pkg.exports);
  }
  // OpenCode 1.x (and magpie's host) load main first; exports["."] is often
  // the OpenCode 2.x wrapper, whose helper exports are not plugin functions.
  if (pkg.main) candidates.unshift(pkg.main);
  if (pkg.module) candidates.push(pkg.module);
  if (pkg.octopus?.entry) candidates.unshift(pkg.octopus.entry);
  candidates.push("index.mjs", "index.js", "index.cjs");

  let entry = "";
  for (const candidate of candidates) {
    const full = path.resolve(dir, candidate);
    if (fs.existsSync(full) && fs.statSync(full).isFile()) {
      entry = full;
      break;
    }
  }
  if (!entry) throw new Error("plugin package has no loadable entry file");

  const mod = await import(`${pathToFileURL(entry).href}?v=${Date.now()}`);
  const functions = [];
  const seen = new Set();
  const add = (fn) => {
    if (typeof fn === "function" && !seen.has(fn)) {
      seen.add(fn);
      functions.push(fn);
    }
  };
  if (typeof mod.default === "function") add(mod.default);
  else if (mod.default && typeof mod.default.server === "function") add(mod.default.server);
  for (const value of Object.values(mod)) add(value);
  if (functions.length === 0) throw new Error("plugin package exports no plugin function");
  return { pkg, entry, functions };
}

async function loadPlugin(entry) {
  if (!entry.enabled) return;
  const id = entry.id || entry.spec;
  try {
    const { pkg, functions } = await importPluginFunctions(entry.dir);
    const loaded = { entry, pkg, functions: [], hooks: null, error: "" };
    const hooksList = [];
    for (const fn of functions) {
      try {
        const hooks = await fn({ client: makeClient(), magpie: makeMagpie(entry), octopus: { dataDir: path.join(DATA_DIR, "state", hash(id)) } });
        if (hooks && typeof hooks === "object") {
          loaded.functions.push(fn);
          hooksList.push(hooks);
        }
      } catch (err) {
        logLine("warn", `${id}: plugin function failed: ${err?.message ?? err}`);
      }
    }
    if (hooksList.length === 0) throw new Error("no plugin hooks returned");
    loaded.hooks = hooksList;
    plugins.set(id, loaded);
    logLine("info", `${id}@${pkg.version || entry.version || "?"} loaded`);
  } catch (err) {
    plugins.set(id, { entry, error: err?.message ?? String(err), hooks: [] });
    logLine("error", `${id}: ${err?.message ?? err}`);
  }
}

function runConfigHooks() {
  config = { provider: {} };
  for (const plugin of plugins.values()) {
    for (const hooks of plugin.hooks || []) {
      if (typeof hooks.config !== "function") continue;
      try {
        hooks.config(config);
      } catch (err) {
        logLine("warn", `${plugin.entry.id}: config hook failed: ${err?.message ?? err}`);
      }
    }
  }
}

function rebuildProviders() {
  providers.clear();
  for (const [pluginID, plugin] of plugins) {
    for (const hooks of plugin.hooks || []) {
      const providerID = hooks.auth?.provider || hooks.provider?.id;
      if (!providerID) continue;
      const cfg = config.provider?.[providerID] || {};
      providers.set(providerKey(pluginID, providerID), {
        pluginID,
        providerID,
        entry: plugin.entry,
        hooks,
        config: cfg,
        name: cfg.name || providerID,
        loaderFetch: null,
        modelCache: null,
      });
    }
  }
}

async function loadAll() {
  plugins.clear();
  providers.clear();
  const installed = readJSON(INSTALLED_FILE, { plugins: [] });
  for (const entry of installed.plugins || []) {
    if (entry.enabled) await loadPlugin(entry);
  }
  runConfigHooks();
  rebuildProviders();
}

function normalizeModelList(raw, cfg) {
  const base = cfg?.models && typeof cfg.models === "object" ? cfg.models : {};
  let values = [];
  if (Array.isArray(raw)) {
    values = raw.map((m) => [m?.id, m]);
  } else if (raw && typeof raw === "object") {
    values = Object.entries(raw);
  }
  const result = [];
  for (const [key, value] of values) {
    if (!value || typeof value !== "object") continue;
    const id = value.id || key;
    if (!id) continue;
    const protocol = value.api?.npm === "@ai-sdk/anthropic" || value.provider?.npm === "@ai-sdk/anthropic"
      ? "anthropic"
      : "openai-chat";
    const target = value.api?.url || value.provider?.api || cfg?.api || "";
    const context = Number(value.limit?.context ?? value.context ?? 0) || 0;
    const output = Number(value.limit?.output ?? value.output ?? 0) || 0;
    result.push({
      id,
      name: value.name || id,
      protocol,
      target,
      context,
      output,
      capabilities: value.capabilities || undefined,
      free: value.free === true,
    });
  }
  // If a live hook returned nothing useful, fall back to the config catalogue.
  if (result.length === 0 && Object.keys(base).length > 0) {
    return normalizeModelList(base, { ...cfg, models: {} });
  }
  return result;
}

async function providerModels(provider, refresh = false) {
  const now = Date.now();
  if (!refresh && provider.modelCache && now - provider.modelCache.at < 30_000) {
    return provider.modelCache;
  }
  let raw = null;
  let error = "";
  try {
    if (typeof provider.hooks.provider?.models === "function") {
      raw = await provider.hooks.provider.models(
        { ...provider.config, id: provider.providerID },
        { auth: authFor(provider.providerID) }
      );
    } else {
      raw = provider.config?.models || {};
    }
  } catch (err) {
    error = err?.message ?? String(err);
    raw = provider.config?.models || {};
  }
  const models = normalizeModelList(raw, provider.config);
  const routes = buildRoutes(provider, models);
  const value = { at: now, models, routes, error };
  provider.modelCache = value;
  return value;
}

function buildRoutes(provider, models) {
  const groups = new Map();
  for (const model of models) {
    const target = model.target || provider.config?.api || "";
    if (!target) continue;
    const key = `${target}|${model.protocol}`;
    let group = groups.get(key);
    if (!group) {
      group = { id: hash(key), protocol: model.protocol, base_url: target, models: [] };
      groups.set(key, group);
    }
    group.models.push(model.id);
  }
  return [...groups.values()];
}

async function providerInfo(provider, refresh = false) {
  if (provider.managed) return provider.info;
  const loggedIn = isLoggedIn(provider.providerID);
  if (!loggedIn) {
    // The plugin's config catalogue is a fallback for its own logic, not an
    // account's authorized model list. Do not expose it as usable models
    // until the provider is signed in.
    return {
      plugin_id: provider.pluginID,
      provider_id: provider.providerID,
      name: provider.name,
      logged_in: false,
      account: "",
      error: "",
      models: [],
      routes: [],
    };
  }
  const { models, routes, error } = await providerModels(provider, refresh);
  return {
    plugin_id: provider.pluginID,
    provider_id: provider.providerID,
    name: provider.name,
    logged_in: true,
    account: authLabel(provider.providerID),
    error: error || "",
    models,
    routes,
  };
}

async function allProviderInfo(refresh = false) {
  await refreshManagedProviders(refresh);
  const result = [];
  for (const provider of allProviders()) {
    result.push(await providerInfo(provider, refresh));
  }
  return result.sort((a, b) => `${a.plugin_id}/${a.provider_id}`.localeCompare(`${b.plugin_id}/${b.provider_id}`));
}

// 自管型插件只暴露公开渠道描述；账号、凭据及其生命周期均由插件实现。
function managedBridge(pluginID) {
  const plugin = plugins.get(pluginID);
  return (plugin?.hooks || []).map((hooks) => hooks.octopus).find((bridge) => typeof bridge?.providers === "function");
}

async function refreshManagedProviders(refresh = false) {
  for (const [pluginID, plugin] of plugins) {
    const bridge = managedBridge(pluginID);
    if (!bridge) continue;
    const descriptors = await bridge.providers({ refresh });
    if (!Array.isArray(descriptors)) throw new Error("plugin providers must return an array");
    const seen = new Set();
    for (const descriptor of descriptors) {
      if (!descriptor?.provider_id || !Array.isArray(descriptor.models) || !Array.isArray(descriptor.routes)) {
        throw new Error("plugin returned an invalid provider descriptor");
      }
      const key = providerKey(pluginID, descriptor.provider_id);
      if (seen.has(key)) throw new Error("plugin provider ID must be unique");
      seen.add(key);
      const info = { ...descriptor, plugin_id: pluginID, plugin_managed: true };
      providers.set(key, {
        pluginID, providerID: info.provider_id, name: info.name,
        entry: plugin.entry, hooks: {}, managed: bridge, info,
        modelCache: { models: info.models, routes: info.routes },
      });
    }
    for (const [key, provider] of providers) {
      if (provider.pluginID === pluginID && provider.managed && !seen.has(key)) providers.delete(key);
    }
  }
}

function providerLoggedIn(provider) {
  return provider.managed ? provider.info.logged_in === true : isLoggedIn(provider.providerID);
}

async function callPlugin(body) {
  if (typeof body.method !== "string" || !/^[a-z][a-z0-9._-]{0,63}$/.test(body.method)) throw new Error("invalid plugin method");
  const bridge = managedBridge(body.plugin_id);
  if (typeof bridge?.call !== "function") throw new Error("plugin does not support page calls");
  const result = await bridge.call({ method: body.method, params: body.params || {} });
  return { data: result?.data ?? null, refresh: result?.refresh === true };
}

function pluginSummaries() {
  return [...plugins.entries()].map(([id, plugin]) => ({
    id,
    spec: plugin.entry?.spec || id,
    version: plugin.pkg?.version || plugin.entry?.version || "",
    enabled: plugin.entry?.enabled !== false,
    error: plugin.error || "",
    providers: [...providers.values()].filter((p) => p.pluginID === id).map((p) => p.providerID),
  }));
}

async function refreshAuth(provider) {
  const auth = authFor(provider.providerID);
  if (!auth || auth.type !== "oauth" || !auth.expires) return auth;
  const lead = Number(provider.hooks.auth?.refreshLead ?? 5 * 60 * 1000) || 5 * 60 * 1000;
  if (auth.expires > Date.now() + lead) return auth;
  const refresh = provider.hooks.auth?.refresh;
  if (typeof refresh !== "function") return auth;
  try {
    const next = await refresh(JSON.parse(JSON.stringify(auth)), provider.providerID);
    if (next && typeof next === "object") {
      const merged = { ...auth, ...next, type: "oauth" };
      saveAuth(provider.providerID, merged);
      return merged;
    }
  } catch (err) {
    logLine("warn", `${provider.providerID}: token refresh failed: ${err?.message ?? err}`);
  }
  return auth;
}

async function loaderFor(provider) {
  if (provider.loaderFetch) return provider.loaderFetch;
  if (typeof provider.hooks.auth?.loader !== "function") return null;
  await refreshAuth(provider);
  const loader = await provider.hooks.auth.loader(async () => authFor(provider.providerID));
  provider.loaderFetch = typeof loader?.fetch === "function" ? loader.fetch : null;
  return provider.loaderFetch;
}

function json(res, status, value) {
  const body = Buffer.from(JSON.stringify(value));
  res.writeHead(status, {
    "content-type": "application/json; charset=utf-8",
    "content-length": body.length,
    "cache-control": "no-store",
  });
  res.end(body);
}

function authorized(req) {
  return req.headers.authorization === `Bearer ${TOKEN}`;
}

async function readBody(req, limit = 64 << 20) {
  const chunks = [];
  let total = 0;
  for await (const chunk of req) {
    total += chunk.length;
    if (total > limit) throw new Error("request body too large");
    chunks.push(chunk);
  }
  return Buffer.concat(chunks);
}

async function parseJSONBody(req) {
  const body = await readBody(req, 4 << 20);
  if (body.length === 0) return {};
  return JSON.parse(body.toString("utf8"));
}

function newSession(provider, url, instructions) {
  const id = crypto.randomBytes(16).toString("hex");
  const session = {
    id,
    providerID: provider.providerID,
    pluginID: provider.pluginID,
    status: "pending",
    url: url || "",
    instructions: instructions || "",
    account: "",
    error: "",
    createdAt: Date.now(),
  };
  sessions.set(id, session);
  return session;
}

function finishSession(session, result) {
  try {
    if (!result || result.type === "failed") throw new Error(result?.error || "sign-in failed");
    setAuthFromResult(session.providerID, result);
    session.status = "success";
    session.account = authLabel(session.providerID);
    const provider = getProvider(session.pluginID, session.providerID);
    if (provider) provider.modelCache = null;
  } catch (err) {
    session.status = "failed";
    session.error = err?.message ?? String(err);
  }
}

function authMethodsFor(provider) {
  const methods = provider.hooks.auth?.methods || [];
  return methods.map((m) => ({
    type: m.type || "api",
    label: m.label || "Sign in",
    placeholder: m.placeholder || "",
  }));
}

async function handleAuthStart(body) {
  const provider = getProvider(body.plugin_id, body.provider_id);
  if (!provider) throw new Error("provider not found");
  if (provider.managed) throw new Error("manage authentication in the plugin page");
  const methods = provider.hooks.auth?.methods || [];
  const method = methods[Number(body.method) || 0];
  if (!method) throw new Error("sign-in method not found");

  if (method.type === "api" && typeof method.authorize !== "function") {
    if (!body.api_key) throw new Error("API key is required");
    saveAuth(provider.providerID, { type: "api", key: body.api_key, accountId: "API key" });
    provider.modelCache = null;
    return { status: "success", account: "API key" };
  }
  if (typeof method.authorize !== "function") throw new Error("sign-in method is not available");

  const result = await method.authorize(body.api_key ? { key: body.api_key } : undefined);
  if (!result || typeof result !== "object") throw new Error("plugin returned no sign-in session");
  if (result.type === "success" || result.type === "failed" || result.key) {
    if (result.type === "failed") throw new Error(result.error || "sign-in failed");
    setAuthFromResult(provider.providerID, result);
    provider.modelCache = null;
    return { status: "success", account: authLabel(provider.providerID) };
  }

  const session = newSession(provider, result.url, result.instructions);
  if (typeof result.callback === "function") {
    Promise.resolve()
      .then(() => result.callback())
      .then((value) => finishSession(session, value))
      .catch((err) => {
        session.status = "failed";
        session.error = err?.message ?? String(err);
      });
  }
  return { status: "pending", url: session.url, instructions: session.instructions, session_id: session.id };
}

async function handleAuthStatus(sessionID) {
  const session = sessions.get(sessionID);
  if (!session) throw new Error("sign-in session not found");
  if (session.status === "pending" && Date.now() - session.createdAt > 10 * 60_000) {
    session.status = "failed";
    session.error = "sign-in timed out";
  }
  return {
    status: session.status,
    url: session.url,
    error: session.error,
    account: session.account,
  };
}

function findRoute(routeID) {
  for (const provider of providers.values()) {
    const route = (provider.modelCache?.routes || []).find((r) => r.id === routeID);
    if (route) return { provider, route };
  }
  return null;
}

async function handleDataPlane(req, res, segments) {
  const routeID = decodeURIComponent(segments[1] || "");
  const rest = segments.slice(3).join("/"); // drop p, route, v1
  // A route id is globally unique across providers. If it is not in a cache
  // yet (the first request after a restart), refresh every provider once.
  if (!findRoute(routeID)) {
    await refreshManagedProviders(false);
    for (const provider of providers.values()) {
      if (!provider.managed && !provider.modelCache) await providerModels(provider, false);
    }
  }
  const found = findRoute(routeID);
  if (!found?.route) {
    json(res, 404, { error: { message: "plugin route not found" } });
    return;
  }
  const provider = found.provider;
  const route = found.route;
  if (!providerLoggedIn(provider)) {
    json(res, 401, { error: { message: `${provider.name} is not signed in` } });
    return;
  }
  const qs = req.url.includes("?") ? req.url.slice(req.url.indexOf("?")) : "";
  const target = `${String(route.base_url).replace(/\/+$/, "")}/${rest}${qs}`;
  const body = await readBody(req);
  const headers = new Headers();
  for (const [key, value] of Object.entries(req.headers)) {
    if (["host", "authorization", "content-length", "connection", "accept-encoding", "transfer-encoding", "expect", "te"].includes(key)) continue;
    if (Array.isArray(value)) for (const v of value) headers.append(key, v);
    else if (value != null) headers.set(key, value);
  }
  if (typeof provider.hooks["chat.headers"] === "function") {
    try {
      const out = { headers: {} };
      await provider.hooks["chat.headers"](
        {
          model: { providerID: provider.providerID },
          provider: { info: { id: provider.providerID } },
          sessionID: headers.get("x-octopus-session") || headers.get("x-request-id") || "",
        },
        out
      );
      for (const [key, value] of Object.entries(out.headers || {})) {
        if (value != null) headers.set(key, String(value));
      }
    } catch (err) {
      logLine("warn", `${provider.name}: chat.headers failed: ${err?.message ?? err}`);
    }
  }
  const request = new Request(target, {
    method: req.method,
    headers,
    body: body.length ? body : undefined,
  });
  try {
    const fetchFn = provider.managed
      ? (request) => provider.managed.fetch({ provider_id: provider.providerID, route_id: route.id, request })
      : await loaderFor(provider);
    if (!fetchFn) throw new Error("plugin has no fetch adapter");
    const response = await fetchFn(request);
    if (!response || typeof response.status !== "number") throw new Error("plugin returned no response");
    const outHeaders = {};
    response.headers.forEach((value, key) => {
      if (["content-length", "content-encoding", "transfer-encoding", "connection"].includes(key)) return;
      outHeaders[key] = value;
    });
    outHeaders["cache-control"] ||= "no-store";
    res.writeHead(response.status, outHeaders);
    if (response.body) {
      Readable.fromWeb(response.body).pipe(res);
    } else {
      res.end();
    }
  } catch (err) {
    const cause = err?.cause ? (err.cause.code || err.cause.message || String(err.cause)) : "";
    logLine("warn", `${provider.name} fetch failed target=${target} bytes=${body.length}${cause ? ` cause=${cause}` : ""}: ${err?.message || err}`);
    if (!res.headersSent) json(res, 502, { error: { message: err?.message ?? String(err) } });
    else res.end();
  }
}

const server = http.createServer(async (req, res) => {
  try {
    const url = new URL(req.url, "http://127.0.0.1");
    const pathname = url.pathname;
    if (pathname === "/healthz") return json(res, 200, { ok: true });
    if (!authorized(req)) return json(res, 401, { error: { message: "unauthorized" } });

    if (pathname === "/status" && req.method === "GET") {
      return json(res, 200, {
        ok: true,
        plugins: pluginSummaries(),
        providers: allProviders().map((p) => ({
          plugin_id: p.pluginID,
          provider_id: p.providerID,
          name: p.name,
          logged_in: providerLoggedIn(p),
          account: p.managed ? p.info.account : authLabel(p.providerID),
        })),
      });
    }
    if (pathname === "/call" && req.method === "POST") return json(res, 200, await callPlugin(await parseJSONBody(req)));
    if (pathname === "/providers" && req.method === "GET") {
      const refresh = url.searchParams.get("refresh") === "1";
      const list = await allProviderInfo(refresh);
      return json(res, 200, { plugins: pluginSummaries(), providers: list });
    }
    if (pathname === "/auth/methods" && req.method === "GET") {
      const provider = getProvider(url.searchParams.get("plugin_id"), url.searchParams.get("provider_id"));
      if (!provider) return json(res, 404, { error: { message: "provider not found" } });
      return json(res, 200, { methods: authMethodsFor(provider) });
    }
    if (pathname === "/auth/start" && req.method === "POST") {
      const body = await parseJSONBody(req);
      return json(res, 200, await handleAuthStart(body));
    }
    if (pathname === "/auth/status" && req.method === "GET") {
      return json(res, 200, await handleAuthStatus(url.searchParams.get("session_id")));
    }
    if (pathname === "/auth/logout" && req.method === "POST") {
      const body = await parseJSONBody(req);
      const provider = getProvider(body.plugin_id, body.provider_id);
      if (provider?.managed) return json(res, 400, { error: { message: "manage authentication in the plugin page" } });
      clearAuth(body.provider_id);
      if (provider) provider.modelCache = null;
      return json(res, 200, { ok: true });
    }
    if (pathname === "/logs" && req.method === "GET") {
      return json(res, 200, { lines: logs });
    }
    if (pathname.startsWith("/p/")) {
      const segments = pathname.split("/").filter(Boolean);
      if (segments.length >= 4) return await handleDataPlane(req, res, segments);
    }
    return json(res, 404, { error: { message: "not found" } });
  } catch (err) {
    logLine("error", `${req.method} ${req.url}: ${err?.stack || err?.message || err}`);
    if (!res.headersSent) json(res, 500, { error: { message: err?.message ?? String(err) } });
    else res.end();
  }
});

process.on("SIGTERM", () => {
  server.close(() => process.exit(0));
});
process.on("SIGINT", () => {
  server.close(() => process.exit(0));
});
process.on("unhandledRejection", (err) => logLine("error", `unhandled rejection: ${err?.message ?? err}`));

await loadAll();
server.listen(0, "127.0.0.1", () => {
  const address = server.address();
  process.stdout.write(`OCTOPUS_PLUGIN_HOST_READY ${JSON.stringify({ port: address.port, token: TOKEN })}\n`);
});
