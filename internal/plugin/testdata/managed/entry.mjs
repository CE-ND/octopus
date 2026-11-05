import fs from "node:fs";
import path from "node:path";

// 通用自管插件测试：宿主不知道 state 内容，也不保存供应商凭据。
export default async function ManagedPlugin({ client, octopus }) {
  fs.mkdirSync(octopus.dataDir, { recursive: true });
  const credentialFile = path.join(octopus.dataDir, "credential.json");
  let credential;
  try { credential = JSON.parse(fs.readFileSync(credentialFile, "utf8")); }
  catch (error) {
    if (error.code !== "ENOENT") throw error;
    const legacy = await client.auth.get({ path: { id: "legacy-managed-resource" } });
    credential = legacy.key;
    fs.writeFileSync(credentialFile, JSON.stringify(credential));
    await client.auth.set({ path: { id: "legacy-managed-resource" }, body: null });
  }
  const file = path.join(octopus.dataDir, "state.json");
  let state = { enabled: true, name: "Managed resource" };
  try { state = JSON.parse(fs.readFileSync(file, "utf8")); } catch (error) { if (error.code !== "ENOENT") throw error; }
  return {
    octopus: {
      async providers() {
        return [{
          provider_id: "managed-resource", name: state.name, account: "Public label", logged_in: state.enabled, error: "",
          models: state.enabled ? [{ id: "managed-model", name: "Managed model", protocol: "anthropic" }] : [],
          routes: state.enabled ? [{ id: "managed-route", protocol: "anthropic", base_url: process.env.MANAGED_TEST_UPSTREAM, models: ["managed-model"] }] : [],
        }];
      },
      async call({ method, params }) {
        if (method === "resource.update") {
          state = { ...state, ...params };
          fs.writeFileSync(file, JSON.stringify(state));
          return { data: { updated: true }, refresh: true };
        }
        if (method === "resource.read") return { data: { ...state } };
        throw new Error("Unknown managed resource operation");
      },
      async fetch({ provider_id, route_id, request }) {
        if (provider_id !== "managed-resource" || route_id !== "managed-route") throw new Error("Wrong managed route");
        const headers = new Headers(request.headers);
        headers.set("x-test-plugin-credential", credential);
        return fetch(new Request(request, { headers }));
      },
    },
  };
}
