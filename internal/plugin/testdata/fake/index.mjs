// A minimal provider plugin used by host and channel-sync tests.
const upstream = (process.env.FAKE_UPSTREAM || "http://127.0.0.1:18080/v1").replace(/\/+$/, "");

export const FakePlugin = async () => ({
  config(config) {
    config.provider ??= {};
    config.provider.fake ??= {};
    config.provider.fake.name = "Fake Plugin";
    config.provider.fake.api = upstream;
    config.provider.fake.models ??= {
      "fake-model": {
        name: "Fake Model",
        limit: { context: 8192, output: 1024 },
        tool_call: true,
      },
    };
  },
  provider: {
    async models(provider) {
      return provider.models;
    },
  },
  auth: {
    provider: "fake",
    methods: [
      { type: "api", label: "Fake API key", placeholder: "sk-fake" },
    ],
    async loader(getAuth) {
      const auth = await getAuth();
      return {
        async fetch(input, init) {
          const req = input instanceof Request ? input : null;
          const headers = new Headers(init?.headers ?? req?.headers);
          if (auth?.key) headers.set("Authorization", `Bearer ${auth.key}`);
          return fetch(req ? req.url : String(input), {
            ...init,
            method: init?.method ?? req?.method,
            headers,
            body: init?.body ?? (req ? await req.clone().text() : undefined),
          });
        },
      };
    },
  },
});
