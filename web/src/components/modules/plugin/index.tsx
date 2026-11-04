'use client';

import { useEffect, useMemo, useState } from 'react';
import { useTranslations } from 'next-intl';
import { useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, CheckCircle2, ChevronDown, LayoutDashboard, Loader2, LogIn, LogOut, Puzzle, RefreshCw, Settings2, Store, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import {
    useInstallPlugin,
    useLogoutPlugin,
    usePluginAuthMethods,
    usePluginConfig,
    usePluginLoginStatus,
    usePluginMarket,
    usePluginPage,
    usePluginProviders,
    usePluginStatus,
    useRefreshPluginModels,
    useRemovePlugin,
    useSavePluginConfig,
    useStartPluginLogin,
    useTogglePlugin,
    type MarketPlugin,
    type PluginAuthMethod,
    type PluginConfigProperty,
    type PluginInfo,
    type PluginProvider,
    type PluginUIPage,
} from '@/api/endpoints/plugin';

export function Plugin() {
    const t = useTranslations('plugin');
    const status = usePluginStatus();
    const providers = usePluginProviders();
    const install = useInstallPlugin();
    const [spec, setSpec] = useState('');
    const [marketRefresh, setMarketRefresh] = useState(0);
    const market = usePluginMarket(marketRefresh);
    const [loginTarget, setLoginTarget] = useState<{ pluginID: string; providerID: string; name: string } | null>(null);
    const [configTarget, setConfigTarget] = useState<PluginInfo | null>(null);
    const [pageTarget, setPageTarget] = useState<{ pluginID: string; page: PluginUIPage } | null>(null);
    const [manageTarget, setManageTarget] = useState<PluginInfo | null>(null);

    const providersByPlugin = useMemo(() => {
        const map = new Map<string, PluginProvider[]>();
        for (const provider of providers.data ?? []) {
            const list = map.get(provider.plugin_id) ?? [];
            list.push(provider);
            map.set(provider.plugin_id, list);
        }
        return map;
    }, [providers.data]);

    const marketPluginIDs = useMemo(
        () => new Set((market.data ?? []).map((item) => item.id)),
        [market.data],
    );
    const visiblePlugins = (status.data?.plugins ?? []).filter((plugin) => !marketPluginIDs.has(plugin.id));

    const handleInstall = () => {
        const value = spec.trim();
        if (!value) return;
        install.mutate(value, {
            onSuccess: () => setSpec(''),
        });
    };

    return (
        <div className="h-full min-h-0 overflow-y-auto overscroll-contain rounded-t-3xl">
            <div className="mx-auto flex w-full max-w-5xl flex-col gap-5 pb-24 pt-2">
                <div className="rounded-3xl border border-border bg-card p-6">
                    <div className="flex items-start gap-3">
                        <div className="rounded-2xl bg-primary/10 p-2 text-primary">
                            <Puzzle className="size-5" />
                        </div>
                        <div className="min-w-0 flex-1">
                            <h1 className="text-lg font-bold text-card-foreground">{t('title')}</h1>
                            <p className="mt-1 text-sm text-muted-foreground">{t('description')}</p>
                        </div>
                    </div>

                    <div className="mt-4 flex flex-col gap-2 sm:flex-row">
                        <Input
                            value={spec}
                            onChange={(event) => setSpec(event.target.value)}
                            placeholder={t('installPlaceholder')}
                            className="h-10 rounded-xl"
                            onKeyDown={(event) => {
                                if (event.key === 'Enter') handleInstall();
                            }}
                        />
                        <Button className="h-10 rounded-xl" onClick={handleInstall} disabled={install.isPending || !spec.trim()}>
                            {install.isPending && <Loader2 className="size-4 animate-spin" />}
                            {t('install')}
                        </Button>
                    </div>
                    {install.error && (
                        <p className="mt-2 text-sm text-destructive">{install.error.message}</p>
                    )}
                </div>

                <PluginMarketplace
                    market={market}
                    onRefresh={() => setMarketRefresh((value) => value + 1)}
                    installedPlugins={status.data?.plugins ?? []}
                    onManage={setManageTarget}
                />

                {status.data?.host_error && (
                    <div className="flex items-start gap-3 rounded-2xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-700 dark:text-amber-300">
                        <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                        <span>{status.data.host_error}</span>
                    </div>
                )}

                {status.isLoading && (
                    <div className="flex items-center gap-2 text-sm text-muted-foreground">
                        <Loader2 className="size-4 animate-spin" />
                        {t('loading')}
                    </div>
                )}

                {!status.isLoading && (status.data?.plugins?.length ?? 0) === 0 && (
                    <div className="rounded-3xl border border-dashed border-border p-10 text-center text-sm text-muted-foreground">
                        {t('empty')}
                    </div>
                )}

                <div className="space-y-4">
                    {visiblePlugins.map((item) => (
                        <PluginCard
                            key={item.id}
                            plugin={item}
                            providers={providersByPlugin.get(item.id) ?? []}
                            onLogin={(provider) => setLoginTarget({ pluginID: item.id, providerID: provider.provider_id, name: provider.name })}
                            onConfig={() => setConfigTarget(item)}
                            onPage={(page) => setPageTarget({ pluginID: item.id, page })}
                        />
                    ))}
                </div>
            </div>

            {loginTarget && (
                <LoginDialog
                    open={!!loginTarget}
                    onOpenChange={(open) => {
                        if (!open) setLoginTarget(null);
                    }}
                    pluginID={loginTarget.pluginID}
                    providerID={loginTarget.providerID}
                    providerName={loginTarget.name}
                />
            )}

            {configTarget && (
                <PluginConfigDialog
                    open={!!configTarget}
                    plugin={configTarget}
                    onOpenChange={(open) => {
                        if (!open) setConfigTarget(null);
                    }}
                />
            )}

            {pageTarget && (
                <PluginPageDialog
                    open={!!pageTarget}
                    pluginID={pageTarget.pluginID}
                    page={pageTarget.page}
                    onOpenChange={(open) => {
                        if (!open) setPageTarget(null);
                    }}
                />
            )}

            {manageTarget && (
                <Dialog
                    open={!!manageTarget}
                    onOpenChange={(open) => {
                        if (!open) setManageTarget(null);
                    }}
                >
                    <DialogContent className="sm:max-w-6xl sm:aspect-[4/3] sm:h-auto rounded-3xl p-0 overflow-hidden">
                        <div className="flex h-full min-h-0 flex-col">
                            <DialogHeader className="border-b border-border px-6 pb-4 pr-14 pt-6">
                                <DialogTitle className="text-xl">{manageTarget.display_name || manageTarget.id}</DialogTitle>
                                <DialogDescription>{t('manageDescription')}</DialogDescription>
                            </DialogHeader>
                            <div className="min-h-0 overflow-y-auto px-6 py-5">
                                <PluginCard
                                    key={manageTarget.id}
                                    plugin={manageTarget}
                                    providers={providersByPlugin.get(manageTarget.id) ?? []}
                                    defaultExpanded
                                    embedded
                                    onLogin={(provider) => {
                                        const target = manageTarget;
                                        setManageTarget(null);
                                        setLoginTarget({ pluginID: target.id, providerID: provider.provider_id, name: provider.name });
                                    }}
                                    onConfig={() => {
                                        const target = manageTarget;
                                        setManageTarget(null);
                                        setConfigTarget(target);
                                    }}
                                    onPage={(page) => {
                                        const target = manageTarget;
                                        setManageTarget(null);
                                        setPageTarget({ pluginID: target.id, page });
                                    }}
                                />
                            </div>
                        </div>
                    </DialogContent>
                </Dialog>
            )}
        </div>
    );
}

function PluginMarketplace({
    market,
    onRefresh,
    installedPlugins,
    onManage,
}: {
    market: ReturnType<typeof usePluginMarket>;
    onRefresh: () => void;
    installedPlugins: PluginInfo[];
    onManage: (plugin: PluginInfo) => void;
}) {
    const t = useTranslations('plugin');
    const install = useInstallPlugin();
    const [installingID, setInstallingID] = useState<string | null>(null);

    const installedByID = useMemo(
        () => new Map(installedPlugins.map((plugin) => [plugin.id, plugin])),
        [installedPlugins],
    );

    const handleInstall = (item: MarketPlugin) => {
        setInstallingID(item.id);
        install.mutate(item.spec, {
            onSettled: () => setInstallingID(null),
        });
    };

    const items = market.data ?? [];

    return (
        <div className="rounded-3xl border border-border bg-card p-5">
            <div className="flex items-start justify-between gap-3">
                <div className="flex items-start gap-3">
                    <div className="rounded-2xl bg-primary/10 p-2 text-primary">
                        <Store className="size-5" />
                    </div>
                    <div className="min-w-0">
                        <h2 className="text-base font-semibold text-card-foreground">{t('marketplaceTitle')}</h2>
                        <p className="mt-1 text-sm text-muted-foreground">{t('marketplaceDescription')}</p>
                    </div>
                </div>
                <Button
                    variant="ghost"
                    size="icon-sm"
                    title={t('marketplaceRefresh')}
                    onClick={onRefresh}
                    disabled={market.isFetching}
                >
                    <RefreshCw className={`size-4 ${market.isFetching ? 'animate-spin' : ''}`} />
                </Button>
            </div>

            {market.isLoading && (
                <div className="mt-4 flex items-center gap-2 text-sm text-muted-foreground">
                    <Loader2 className="size-4 animate-spin" />
                    {t('loading')}
                </div>
            )}

            {market.error && (
                <p className="mt-4 text-sm text-destructive">{market.error.message}</p>
            )}

            {!market.isLoading && !market.error && items.length === 0 && (
                <div className="mt-4 rounded-2xl border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
                    {t('marketplaceEmpty')}
                </div>
            )}

            {items.length > 0 && (
                <div className="mt-4 grid gap-3 md:grid-cols-2">
                    {items.map((item) => {
                        const current = installedByID.get(item.id);
                        const installed = item.installed || !!current;
                        const currentVersion = item.installed_version || current?.version || '';
                        const upToDate = installed && currentVersion === item.version;
                        const installing = installingID === item.id;
                        const manageable = !!current;
                        const showManage = upToDate && manageable;
                        return (
                            <div
                                key={item.id}
                                className={`flex min-h-40 flex-col rounded-2xl border border-border/70 bg-background/40 p-4 ${manageable ? 'cursor-pointer transition-colors hover:border-primary/40' : ''}`}
                                role={manageable ? 'button' : undefined}
                                tabIndex={manageable ? 0 : undefined}
                                onClick={manageable ? () => onManage(current) : undefined}
                                onKeyDown={manageable ? (event) => {
                                    if (event.key === 'Enter' || event.key === ' ') {
                                        event.preventDefault();
                                        onManage(current);
                                    }
                                } : undefined}
                            >
                                <div className="flex items-start justify-between gap-2">
                                    <div className="min-w-0">
                                        <h3 className="truncate text-sm font-semibold text-card-foreground">
                                            {item.display_name || item.id}
                                        </h3>
                                        <p className="mt-0.5 truncate text-xs text-muted-foreground">{item.id}</p>
                                    </div>
                                    <div className="flex shrink-0 items-center gap-1.5">
                                        <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
                                            v{item.version}
                                        </span>
                                        {upToDate && (
                                            <span className="rounded-full bg-emerald-500/10 px-2 py-0.5 text-xs text-emerald-600 dark:text-emerald-400">
                                                {t('marketplaceInstalled')}
                                            </span>
                                        )}
                                    </div>
                                </div>
                                {item.description && (
                                    <p className="mt-2 line-clamp-3 text-xs leading-5 text-muted-foreground">{item.description}</p>
                                )}
                                <div className="mt-auto flex items-center justify-between gap-2 pt-3">
                                    <span className="truncate text-xs text-muted-foreground">
                                        {item.publisher ? t('marketplacePublisher', { name: item.publisher }) : ''}
                                    </span>
                                    <Button
                                        size="sm"
                                        variant={showManage ? 'secondary' : 'default'}
                                        disabled={installing || (upToDate && !manageable)}
                                        onClick={(event) => {
                                            event.stopPropagation();
                                            if (showManage) {
                                                onManage(current);
                                            } else if (!upToDate) {
                                                handleInstall(item);
                                            }
                                        }}
                                    >
                                        {installing && <Loader2 className="size-3.5 animate-spin" />}
                                        {showManage
                                            ? t('managePlugin')
                                            : upToDate
                                                ? t('marketplaceInstalled')
                                                : installed
                                                    ? t('marketplaceUpdate')
                                                    : t('marketplaceInstall')}
                                    </Button>
                                </div>
                            </div>
                        );
                    })}
                </div>
            )}

            {install.error && (
                <p className="mt-3 text-sm text-destructive">{install.error.message}</p>
            )}
        </div>
    );
}

function PluginCard({
    plugin,
    providers,
    onLogin,
    onConfig,
    onPage,
    defaultExpanded = false,
    embedded = false,
}: {
    plugin: PluginInfo;
    providers: PluginProvider[];
    onLogin: (provider: PluginProvider) => void;
    onConfig: () => void;
    onPage: (page: PluginUIPage) => void;
    defaultExpanded?: boolean;
    embedded?: boolean;
}) {
    const t = useTranslations('plugin');
    const toggle = useTogglePlugin();
    const remove = useRemovePlugin();
    const refresh = useRefreshPluginModels();
    const logout = useLogoutPlugin();
    const [expanded, setExpanded] = useState(defaultExpanded);

    return (
        <div className={embedded ? '' : 'rounded-3xl border border-border bg-card p-5'}>
            <div className="flex flex-wrap items-start justify-between gap-3">
                {embedded ? (
                    <div className="min-w-0">
                        <p className="break-all text-xs text-muted-foreground">{plugin.spec}</p>
                        {plugin.error && <p className="mt-2 text-sm text-destructive">{plugin.error}</p>}
                    </div>
                ) : (
                    <div
                        className="min-w-0 flex-1 cursor-pointer select-none"
                        role="button"
                        tabIndex={0}
                        aria-expanded={expanded}
                        title={expanded ? t('collapseDetails') : t('expandDetails')}
                        onClick={() => setExpanded((value) => !value)}
                        onKeyDown={(event) => {
                            if (event.key === 'Enter' || event.key === ' ') {
                                event.preventDefault();
                                setExpanded((value) => !value);
                            }
                        }}
                    >
                        <div className="flex items-start gap-2">
                            <ChevronDown className={`mt-0.5 size-4 shrink-0 text-muted-foreground transition-transform ${expanded ? 'rotate-180' : ''}`} />
                            <div className="min-w-0">
                                <div className="flex flex-wrap items-center gap-2">
                                    <h2 className="truncate text-base font-semibold text-card-foreground">{plugin.display_name || plugin.id}</h2>
                                    <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">v{plugin.version}</span>
                                    <span className={`rounded-full px-2 py-0.5 text-xs ${plugin.enabled ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' : 'bg-muted text-muted-foreground'}`}>
                                        {plugin.enabled ? t('enabled') : t('disabled')}
                                    </span>
                                </div>
                                <p className="mt-1 break-all text-xs text-muted-foreground">
                                    {plugin.display_name && !plugin.spec.startsWith(plugin.id) ? `${plugin.id} · ${plugin.spec}` : plugin.spec}
                                </p>
                                {plugin.error && <p className="mt-2 text-sm text-destructive">{plugin.error}</p>}
                            </div>
                        </div>
                    </div>
                )}
                <div className="flex items-center gap-2">
                    {plugin.pages?.map((page) => (
                        <Button key={page.id} variant="ghost" size="icon-sm" title={page.title || t('openPage')} onClick={() => onPage(page)}>
                            <LayoutDashboard className="size-4" />
                        </Button>
                    ))}
                    {plugin.config_schema && (
                        <Button variant="ghost" size="icon-sm" title={t('config')} onClick={onConfig}>
                            <Settings2 className="size-4" />
                        </Button>
                    )}
                    <Button variant="ghost" size="icon-sm" title={t('refresh')} onClick={() => refresh.mutate()} disabled={refresh.isPending}>
                        <RefreshCw className={`size-4 ${refresh.isPending ? 'animate-spin' : ''}`} />
                    </Button>
                    <Switch
                        checked={plugin.enabled}
                        onCheckedChange={(checked) => toggle.mutate({ id: plugin.id, enabled: checked })}
                        disabled={toggle.isPending}
                    />
                    <Button
                        variant="ghost"
                        size="icon-sm"
                        title={t('remove')}
                        className="text-destructive hover:text-destructive"
                        onClick={() => {
                            if (window.confirm(t('removeConfirm', { name: plugin.display_name || plugin.id }))) remove.mutate(plugin.id);
                        }}
                        disabled={remove.isPending}
                    >
                        <Trash2 className="size-4" />
                    </Button>
                </div>
            </div>

            {expanded && (
                <div className="mt-4 space-y-2">
                    {providers.length === 0 && (
                    <p className="text-sm text-muted-foreground">{plugin.enabled ? t('noProviders') : t('disabledHint')}</p>
                )}
                {providers.map((provider) => (
                    <div key={provider.provider_id} className="rounded-2xl border border-border/70 bg-background/40 p-3">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                            <div className="flex items-center gap-2">
                                {provider.logged_in
                                    ? <CheckCircle2 className="size-4 text-emerald-500" />
                                    : <span className="size-4 rounded-full border border-muted-foreground/40" />}
                                <span className="text-sm font-medium">{provider.name}</span>
                                <span className="text-xs text-muted-foreground">{provider.provider_id}</span>
                                {provider.logged_in && provider.account && (
                                    <span className="text-xs text-muted-foreground">· {provider.account}</span>
                                )}
                            </div>
                            <div className="flex items-center gap-2">
                                <span className="text-xs text-muted-foreground">
                                    {provider.logged_in ? t('modelCount', { count: provider.models.length }) : t('loginToLoadModels')}
                                </span>
                                {provider.logged_in ? (
                                    <Button
                                        variant="outline"
                                        size="sm"
                                        className="rounded-lg"
                                        onClick={() => logout.mutate({ plugin_id: plugin.id, provider_id: provider.provider_id })}
                                        disabled={logout.isPending}
                                    >
                                        <LogOut className="size-3.5" />
                                        {t('logout')}
                                    </Button>
                                ) : (
                                    <Button size="sm" className="rounded-lg" onClick={() => onLogin(provider)}>
                                        <LogIn className="size-3.5" />
                                        {t('login')}
                                    </Button>
                                )}
                            </div>
                        </div>
                        {provider.error && <p className="mt-2 text-xs text-destructive">{provider.error}</p>}
                        {provider.logged_in && provider.models.length > 0 && (
                            <div className="mt-2 flex flex-wrap gap-1.5">
                                {provider.models.slice(0, 8).map((model) => (
                                    <span key={model.id} className="rounded-md bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
                                        {model.id}
                                        {model.free ? ` · ${t('free')}` : ''}
                                    </span>
                                ))}
                                {provider.models.length > 8 && (
                                    <span className="rounded-md bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
                                        +{provider.models.length - 8}
                                    </span>
                                )}
                            </div>
                        )}
                    </div>
                ))}
                </div>
            )}
        </div>
    );
}

function PluginPageDialog({
    open,
    pluginID,
    page,
    onOpenChange,
}: {
    open: boolean;
    pluginID: string;
    page: PluginUIPage;
    onOpenChange: (open: boolean) => void;
}) {
    const t = useTranslations('plugin');
    const html = usePluginPage(pluginID, page.id, open);
    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-5xl rounded-3xl p-3">
                <DialogHeader className="px-3 pt-2">
                    <DialogTitle>{page.title || t('openPage')}</DialogTitle>
                    <DialogDescription>{t('sandboxHint')}</DialogDescription>
                </DialogHeader>
                {html.isLoading && (
                    <div className="flex items-center gap-2 p-6 text-sm text-muted-foreground">
                        <Loader2 className="size-4 animate-spin" />
                        {t('loading')}
                    </div>
                )}
                {html.error && <p className="p-6 text-sm text-destructive">{html.error.message}</p>}
                {html.data?.html && (
                    <iframe
                        title={page.title || page.id}
                        sandbox="allow-scripts allow-forms"
                        srcDoc={html.data.html}
                        className="h-[70vh] w-full rounded-2xl border border-border bg-background"
                        onLoad={(event) => {
                            event.currentTarget.contentWindow?.postMessage({ type: 'octopus:init', pluginId: pluginID, pageId: page.id }, '*');
                        }}
                    />
                )}
            </DialogContent>
        </Dialog>
    );
}

function PluginConfigDialog({
    open,
    plugin,
    onOpenChange,
}: {
    open: boolean;
    plugin: PluginInfo;
    onOpenChange: (open: boolean) => void;
}) {
    const t = useTranslations('plugin');
    const config = usePluginConfig(plugin.id, open);
    const save = useSavePluginConfig();

    const schema = config.data?.config_schema ?? plugin.config_schema;
    const properties = schema?.properties ?? {};
    const initialOptions = config.data?.options ?? plugin.options ?? {};

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-lg rounded-3xl">
                <DialogHeader>
                    <DialogTitle>{t('configTitle', { name: plugin.display_name || plugin.id })}</DialogTitle>
                    <DialogDescription>{t('configDescription')}</DialogDescription>
                </DialogHeader>
                {config.isLoading && (
                    <div className="flex items-center gap-2 text-sm text-muted-foreground">
                        <Loader2 className="size-4 animate-spin" />
                        {t('loading')}
                    </div>
                )}
                {config.error && <p className="text-sm text-destructive">{config.error.message}</p>}
                {!config.isLoading && !config.error && Object.keys(properties).length === 0 && (
                    <p className="text-sm text-muted-foreground">{t('configEmpty')}</p>
                )}
                {!config.isLoading && !config.error && Object.keys(properties).length > 0 && (
                    <ConfigFields
                        properties={properties}
                        initialOptions={initialOptions}
                        onSubmit={(options) => {
                            save.mutate(
                                { plugin_id: plugin.id, options },
                                { onSuccess: () => onOpenChange(false) }
                            );
                        }}
                        pending={save.isPending}
                        error={save.error?.message}
                        onCancel={() => onOpenChange(false)}
                    />
                )}
            </DialogContent>
        </Dialog>
    );
}

function ConfigFields({
    properties,
    initialOptions,
    onSubmit,
    pending,
    error,
    onCancel,
}: {
    properties: Record<string, PluginConfigProperty>;
    initialOptions: Record<string, unknown>;
    onSubmit: (options: Record<string, unknown>) => void;
    pending: boolean;
    error?: string;
    onCancel: () => void;
}) {
    const t = useTranslations('plugin');
    const [values, setValues] = useState<Record<string, unknown>>(() => {
        const next: Record<string, unknown> = {};
        for (const [key, property] of Object.entries(properties)) {
            next[key] = initialOptions[key] ?? property.default ?? (property.type === 'boolean' ? false : property.type === 'number' || property.type === 'integer' ? 0 : '');
        }
        return next;
    });

    const setValue = (key: string, value: unknown) => {
        setValues((previous) => ({ ...previous, [key]: value }));
    };

    return (
        <div className="space-y-4">
            <div className="max-h-[55vh] space-y-4 overflow-y-auto pr-1">
                {Object.entries(properties).map(([key, property]) => (
                    <div key={key} className="space-y-1.5">
                        <label className="text-sm font-medium" htmlFor={`plugin-config-${key}`}>
                            {property.title || key}
                        </label>
                        {property.type === 'boolean' ? (
                            <div className="flex items-center gap-2">
                                <Switch
                                    id={`plugin-config-${key}`}
                                    checked={values[key] === true}
                                    onCheckedChange={(checked) => setValue(key, checked)}
                                />
                                <span className="text-xs text-muted-foreground">{values[key] === true ? t('enabled') : t('disabled')}</span>
                            </div>
                        ) : property.enum ? (
                            <select
                                id={`plugin-config-${key}`}
                                className="h-10 w-full rounded-xl border border-input bg-background px-3 text-sm"
                                value={String(values[key] ?? '')}
                                onChange={(event) => setValue(key, event.target.value)}
                            >
                                {property.enum.map((option) => (
                                    <option key={String(option)} value={String(option)}>{String(option)}</option>
                                ))}
                            </select>
                        ) : (
                            <Input
                                id={`plugin-config-${key}`}
                                type={property.format === 'password' || property.writeOnly ? 'password' : property.type === 'number' || property.type === 'integer' ? 'number' : 'text'}
                                value={String(values[key] ?? '')}
                                onChange={(event) => setValue(key, property.type === 'number' || property.type === 'integer' ? Number(event.target.value) : event.target.value)}
                                className="h-10 rounded-xl"
                            />
                        )}
                        {property.description && <p className="text-xs text-muted-foreground">{property.description}</p>}
                    </div>
                ))}
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
                <Button variant="outline" className="rounded-xl" onClick={onCancel}>
                    {t('cancel')}
                </Button>
                <Button className="rounded-xl" onClick={() => onSubmit(values)} disabled={pending}>
                    {pending && <Loader2 className="size-4 animate-spin" />}
                    {t('configSave')}
                </Button>
            </DialogFooter>
        </div>
    );
}

function LoginDialog({
    open,
    onOpenChange,
    pluginID,
    providerID,
    providerName,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    pluginID: string;
    providerID: string;
    providerName: string;
}) {
    const t = useTranslations('plugin');
    const qc = useQueryClient();
    const methods = usePluginAuthMethods(pluginID, providerID, open);
    const start = useStartPluginLogin();
    const [methodIndex, setMethodIndex] = useState(0);
    const [apiKey, setApiKey] = useState('');
    const [sessionID, setSessionID] = useState('');
    const status = usePluginLoginStatus(sessionID, !!sessionID);

    const selected: PluginAuthMethod | undefined = methods.data?.[methodIndex];

    useEffect(() => {
        if (status.data?.status === 'success') {
            void qc.invalidateQueries({ queryKey: ['plugin'] });
            onOpenChange(false);
        }
    }, [status.data?.status, qc, onOpenChange]);

    const handleStart = () => {
        if (!selected) return;
        start.mutate(
            {
                plugin_id: pluginID,
                provider_id: providerID,
                method: methodIndex,
                ...(selected.type === 'api' ? { api_key: apiKey } : {}),
            },
            {
                onSuccess: (data) => {
                    if (data.status === 'success') {
                        void qc.invalidateQueries({ queryKey: ['plugin'] });
                        onOpenChange(false);
                        return;
                    }
                    if (data.id) setSessionID(data.id);
                    if (data.url) window.open(data.url, '_blank', 'noopener,noreferrer');
                },
            }
        );
    };

    const waiting = status.data?.status === 'pending' || (!!sessionID && status.isLoading);

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-lg rounded-3xl">
                <DialogHeader>
                    <DialogTitle>{t('loginTitle', { name: providerName })}</DialogTitle>
                    <DialogDescription>{t('loginDescription')}</DialogDescription>
                </DialogHeader>

                {methods.isLoading && (
                    <div className="flex items-center gap-2 text-sm text-muted-foreground">
                        <Loader2 className="size-4 animate-spin" />
                        {t('loading')}
                    </div>
                )}

                {methods.error && <p className="text-sm text-destructive">{methods.error.message}</p>}

                <div className="space-y-2">
                    {(methods.data ?? []).map((method, index) => (
                        <button
                            key={`${method.type}-${method.label}-${index}`}
                            className={`w-full rounded-2xl border p-3 text-left text-sm transition-colors ${
                                index === methodIndex ? 'border-primary bg-primary/5' : 'border-border hover:bg-muted/40'
                            }`}
                            onClick={() => setMethodIndex(index)}
                            disabled={waiting}
                        >
                            <div className="font-medium">{method.label}</div>
                            <div className="mt-0.5 text-xs text-muted-foreground">
                                {method.type === 'api' ? t('apiKeyMethod') : t('oauthMethod')}
                            </div>
                        </button>
                    ))}
                </div>

                {selected?.type === 'api' && (
                    <Input
                        value={apiKey}
                        onChange={(event) => setApiKey(event.target.value)}
                        placeholder={selected.placeholder || t('apiKeyPlaceholder')}
                        className="h-10 rounded-xl"
                        disabled={waiting}
                    />
                )}

                {status.data?.url && (
                    <div className="rounded-2xl border border-border bg-muted/30 p-3 text-sm">
                        <p>{status.data.instructions || t('browserHint')}</p>
                        <a href={status.data.url} target="_blank" rel="noopener noreferrer" className="mt-2 inline-block text-primary hover:underline">
                            {t('openSignIn')}
                        </a>
                    </div>
                )}

                {waiting && (
                    <div className="flex items-center gap-2 text-sm text-muted-foreground">
                        <Loader2 className="size-4 animate-spin" />
                        {t('waiting')}
                    </div>
                )}

                {status.data?.status === 'failed' && <p className="text-sm text-destructive">{status.data.error || t('loginFailed')}</p>}
                {start.error && <p className="text-sm text-destructive">{start.error.message}</p>}

                <DialogFooter>
                    <Button variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)}>
                        {t('cancel')}
                    </Button>
                    <Button
                        className="rounded-xl"
                        onClick={handleStart}
                        disabled={!selected || start.isPending || waiting || (selected?.type === 'api' && !apiKey.trim())}
                    >
                        {start.isPending && <Loader2 className="size-4 animate-spin" />}
                        {t('startLogin')}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
