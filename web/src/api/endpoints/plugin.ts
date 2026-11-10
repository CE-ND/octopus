'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../client';

export type PluginUIPage = {
    id: string;
    title: string;
    entry: string;
    slot?: string;
};

export type PluginConfigProperty = {
    type?: string;
    title?: string;
    description?: string;
    default?: unknown;
    enum?: unknown[];
    format?: string;
    writeOnly?: boolean;
};

export type PluginConfigSchema = {
    type?: string;
    properties?: Record<string, PluginConfigProperty>;
    required?: string[];
};

export type PluginInfo = {
    id: string;
    display_name?: string;
    spec: string;
    version: string;
    enabled: boolean;
    error?: string;
    providers?: string[];
    options?: Record<string, unknown>;
    config_schema?: PluginConfigSchema;
    pages?: PluginUIPage[];
};

export type PluginConfigInfo = {
    plugin_id: string;
    display_name?: string;
    config_schema?: PluginConfigSchema;
    options?: Record<string, unknown>;
    pages?: PluginUIPage[];
};

export type PluginModel = {
    id: string;
    name: string;
    protocol: string;
    target?: string;
    context?: number;
    output?: number;
    free?: boolean;
};

export type PluginRoute = {
    id: string;
    protocol: string;
    base_url: string;
    models: string[];
};

export type PluginProvider = {
    plugin_id: string;
    provider_id: string;
    plugin_managed?: boolean;
    name: string;
    logged_in: boolean;
    account?: string;
    error?: string;
    models: PluginModel[];
    routes: PluginRoute[];
};

export type PluginStatus = {
    enabled: boolean;
    host_running: boolean;
    host_error?: string;
    plugins: PluginInfo[];
    providers: PluginProvider[];
};

export type MarketPlugin = {
    id: string;
    display_name?: string;
    version: string;
    description?: string;
    publisher?: string;
    spec: string;
    homepage?: string;
    installed: boolean;
    installed_version?: string;
    update_available?: boolean;
};

export type PluginAuthMethod = {
    type: string;
    label: string;
    placeholder?: string;
};

export type PluginAuthSession = {
    id?: string;
    status: string;
    url?: string;
    instructions?: string;
    error?: string;
    account?: string;
};

const pluginKeys = {
    status: ['plugin', 'status'] as const,
    providers: ['plugin', 'providers'] as const,
    methods: (pluginID: string, providerID: string) => ['plugin', 'methods', pluginID, providerID] as const,
};

export function usePluginCall() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (input: { plugin_id: string; method: string; params?: unknown }) =>
            apiClient.post<{ data: unknown; refresh: boolean }>('/api/v1/plugin/call', input),
        onSuccess: (result) => {
            if (!result.refresh) return;
            void qc.invalidateQueries({ queryKey: ['plugin'] });
            void qc.invalidateQueries({ queryKey: ['channels'] });
            void qc.invalidateQueries({ queryKey: ['groups'] });
        },
    });
}

export function usePluginStatus() {
    return useQuery({
        queryKey: pluginKeys.status,
        queryFn: () => apiClient.get<PluginStatus>('/api/v1/plugin/list'),
        refetchInterval: 5000,
    });
}

export function usePluginMarket(refresh = 0) {
    return useQuery({
        queryKey: ['plugin', 'market', refresh],
        queryFn: () => apiClient.get<MarketPlugin[]>('/api/v1/plugin/market', refresh > 0 ? { refresh: 1 } : undefined),
        staleTime: 5 * 60 * 1000,
        refetchOnWindowFocus: false,
    });
}

export function usePluginProviders() {
    return useQuery({
        queryKey: pluginKeys.providers,
        queryFn: () => apiClient.get<PluginProvider[]>('/api/v1/plugin/providers'),
        refetchInterval: 60000,
    });
}

export function useInstallPlugin() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (spec: string) => apiClient.post<PluginInfo>('/api/v1/plugin/install', { spec }),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: pluginKeys.status });
            void qc.invalidateQueries({ queryKey: pluginKeys.providers });
            void qc.invalidateQueries({ queryKey: ['plugin', 'page'] });
            void qc.invalidateQueries({ queryKey: ['plugin', 'market'] });
        },
    });
}

export function useTogglePlugin() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (input: { id: string; enabled: boolean }) => apiClient.post<null>('/api/v1/plugin/toggle', input),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: pluginKeys.status });
            void qc.invalidateQueries({ queryKey: pluginKeys.providers });
            void qc.invalidateQueries({ queryKey: ['plugin', 'page'] });
        },
    });
}

export function useRemovePlugin() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => apiClient.post<null>('/api/v1/plugin/remove', { id }),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: pluginKeys.status });
            void qc.invalidateQueries({ queryKey: pluginKeys.providers });
            void qc.invalidateQueries({ queryKey: ['plugin', 'page'] });
            void qc.invalidateQueries({ queryKey: ['plugin', 'market'] });
        },
    });
}

export function usePluginAuthMethods(pluginID: string, providerID: string, enabled = true) {
    return useQuery({
        queryKey: pluginKeys.methods(pluginID, providerID),
        queryFn: () => apiClient.get<PluginAuthMethod[]>('/api/v1/plugin/auth/methods', { plugin_id: pluginID, provider_id: providerID }),
        enabled: enabled && !!pluginID && !!providerID,
    });
}

export function useStartPluginLogin() {
    return useMutation({
        mutationFn: (input: { plugin_id: string; provider_id: string; method: number; api_key?: string }) =>
            apiClient.post<PluginAuthSession>('/api/v1/plugin/auth/start', input),
    });
}

export function usePluginLoginStatus(sessionID: string, enabled: boolean) {
    return useQuery({
        queryKey: ['plugin', 'login', sessionID],
        queryFn: () => apiClient.get<PluginAuthSession>('/api/v1/plugin/auth/status', { session_id: sessionID }),
        enabled: enabled && !!sessionID,
        refetchInterval: (query) => {
            const data = query.state.data;
            return data?.status === 'pending' ? 1000 : false;
        },
    });
}

export function useLogoutPlugin() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (input: { plugin_id: string; provider_id: string }) =>
            apiClient.post<null>('/api/v1/plugin/auth/logout', input),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: pluginKeys.status });
            void qc.invalidateQueries({ queryKey: pluginKeys.providers });
        },
    });
}

export function useRefreshPluginModels() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: () => apiClient.post<PluginProvider[]>('/api/v1/plugin/models/refresh', {}),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: pluginKeys.status });
            void qc.invalidateQueries({ queryKey: pluginKeys.providers });
        },
    });
}

export function usePluginConfig(pluginID: string, enabled: boolean) {
    return useQuery({
        queryKey: ['plugin', 'config', pluginID],
        queryFn: () => apiClient.get<PluginConfigInfo>('/api/v1/plugin/config', { plugin_id: pluginID }),
        enabled,
    });
}

export function useSavePluginConfig() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: (input: { plugin_id: string; options: Record<string, unknown> }) =>
            apiClient.post<null>('/api/v1/plugin/config/save', input),
        onSuccess: () => {
            void qc.invalidateQueries({ queryKey: pluginKeys.status });
            void qc.invalidateQueries({ queryKey: pluginKeys.providers });
            void qc.invalidateQueries({ queryKey: ['plugin', 'config'] });
        },
    });
}

export function usePluginPage(pluginID: string, pageID: string, enabled: boolean, version: string) {
    return useQuery({
        // 页面 HTML 随插件版本变化；版本进入 key，插件升级后不会复用旧 iframe。
        queryKey: ['plugin', 'page', pluginID, pageID, version],
        queryFn: () => apiClient.get<{ html: string }>('/api/v1/plugin/ui/page', { plugin_id: pluginID, page_id: pageID }),
        enabled: enabled && !!pluginID && !!pageID,
        staleTime: 0,
        refetchOnMount: 'always',
    });
}
