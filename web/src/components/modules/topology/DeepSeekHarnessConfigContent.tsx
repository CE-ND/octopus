'use client';

import { useId, useMemo, useRef, useState } from 'react';
import { motion } from 'motion/react';
import { useTranslations } from 'next-intl';
import { Check, FileCode2, KeyRound, LoaderCircle, Plus, SlidersHorizontal, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsList, TabsTrigger } from '@/components/animate-ui/components/animate/tabs';
import { toast } from '@/components/common/Toast';
import { useAPIKeyList, useCreateAPIKey, type APIKey } from '@/api/endpoints/apikey';
import { useGroupList } from '@/api/endpoints/group';
import {
    useAgentAutoConfig,
    useAgentConfig,
    useSaveAgentConfig,
    type DshAPI,
    type DshConfigState,
    type DshPreparedConfig,
    type DshReasoningEffort,
    type DshRouteSelection,
} from '@/api/endpoints/agent-config';
import { AgentConfigHeader } from './AgentConfigHeader';
import { AgentConfigPanel, AgentConfigPanels } from './AgentConfigPanels';
import { AgentConfigTextEditor } from './AgentConfigTextEditor';
import { AgentModelGroupSelect, type AgentModelGroup } from './AgentModelGroupSelect';

const PROFILE = 'desktop';
const NONE = '__keep__';
const APIS: { value: DshAPI; label: string }[] = [
    { value: 'openai-completions', label: 'OpenAI Chat Completions' },
    { value: 'openai-responses', label: 'OpenAI Responses' },
    { value: 'anthropic-messages', label: 'Anthropic Messages' },
    { value: 'google-generative-ai', label: 'Google Generative AI (Gemini)' },
];
const REASONING_EFFORTS: DshReasoningEffort[] = ['default', 'off', 'low', 'high', 'max'];
type View = 'quick' | 'file';
type Draft = { content: string; baseModifiedAt: number };
type PendingCredential = DshPreparedConfig & { api_key_id: number };
type RouteDraft = { key: string; groupId: number; api: DshAPI };

function messageOf(error: unknown, fallback: string): string {
    return error instanceof Error ? error.message : typeof error === 'object' && error !== null && 'message' in error && typeof error.message === 'string' ? error.message : fallback;
}

function isDshAPI(value: string | undefined): value is DshAPI {
    return APIS.some(item => item.value === value);
}

function routeSignature(routes: RouteDraft[]): string {
    return JSON.stringify([...routes]
        .filter(route => route.groupId > 0)
        .sort((left, right) => left.groupId - right.groupId || left.api.localeCompare(right.api))
        .map(route => [route.groupId, route.api]));
}

function routesFromState(state: DshConfigState | undefined, groups: AgentModelGroup[], savedGroupId: number, savedAPI: DshAPI): RouteDraft[] {
    const byId = new Map(groups.map(group => [group.id, group]));
    const byName = new Map(groups.map(group => [group.name, group]));
    const routes = (state?.routes ?? [])
        .map((route, index): RouteDraft => {
            const groupId = route.model_group_id && byId.has(route.model_group_id)
                ? route.model_group_id
                : byName.get(route.model_name)?.id ?? 0;
            return {
                key: `${route.provider}:${route.model_name}:${index}`,
                groupId,
                api: isDshAPI(route.api) ? route.api : 'openai-completions',
            };
        })
        .filter(route => route.groupId > 0);
    if (routes.length > 0) return routes;
    return savedGroupId > 0 ? [{ key: `saved:${savedGroupId}`, groupId: savedGroupId, api: savedAPI }] : [];
}

function routeSelections(routes: RouteDraft[]): DshRouteSelection[] {
    return routes
        .filter(route => route.groupId > 0 && isDshAPI(route.api))
        .map(route => ({ model_group_id: route.groupId, api: route.api }));
}

/** 只编辑 profile YAML；凭据通过密钥 ID 交给后端合并保存。 */
export function DeepSeekHarnessConfigContent() {
    const t = useTranslations('topology');
    const instanceId = useId();
    const quickTabRef = useRef<HTMLButtonElement>(null);
    const fileTabRef = useRef<HTMLButtonElement>(null);
    const configQuery = useAgentConfig('deepseek-harness', PROFILE);
    const keysQuery = useAPIKeyList();
    const groupsQuery = useGroupList();
    const createKey = useCreateAPIKey();
    const autoConfig = useAgentAutoConfig('deepseek-harness');
    const saveConfig = useSaveAgentConfig('deepseek-harness');
    const [view, setView] = useState<View>('quick');
    const [draft, setDraft] = useState<Draft>();
    const [pendingCredential, setPendingCredential] = useState<PendingCredential>();
    const [keyOverride, setKeyOverride] = useState<number>();
    const [routeOverride, setRouteOverride] = useState<RouteDraft[]>();
    const [defaultGroupOverride, setDefaultGroupOverride] = useState<number>();
    const [reasoningOverride, setReasoningOverride] = useState<DshReasoningEffort>();
    const [createdKey, setCreatedKey] = useState<APIKey>();
    const [processing, setProcessing] = useState(false);
    const [conflict, setConflict] = useState(false);

    const file = configQuery.data?.files.find(item => item.key === 'profile');
    const state = configQuery.data?.dsh;
    const content = draft?.content ?? file?.content ?? '';
    const keys = [...(keysQuery.data ?? [])];
    if (createdKey && !keys.some(key => key.id === createdKey.id)) keys.push(createdKey);
    const groups = (groupsQuery.data ?? []).filter((group): group is AgentModelGroup => typeof group.id === 'number' && group.id > 0).sort((a, b) => a.name.localeCompare(b.name));
    const savedGroupId = state?.current_provider?.startsWith('octopus') ? groups.find(group => group.name === state.current_model)?.id ?? 0 : 0;
    const savedAPI = isDshAPI(state?.api) ? state.api : 'openai-completions';
    const baselineRoutes = useMemo(() => routesFromState(state, groups, savedGroupId, savedAPI), [state, groups, savedGroupId, savedAPI]);
    const routes = routeOverride ?? baselineRoutes;
    const savedDefaultGroupId = state?.default_model_group_id ?? savedGroupId;
    const baselineDefaultGroupId = savedDefaultGroupId || baselineRoutes[0]?.groupId || 0;
    const defaultGroupId = defaultGroupOverride ?? pendingCredential?.default_model_group_id ?? baselineDefaultGroupId;
    const savedReasoning = state?.reasoning_effort || 'default';
    const reasoning = reasoningOverride ?? pendingCredential?.reasoning_effort ?? savedReasoning;
    const keyId = keyOverride ?? pendingCredential?.api_key_id ?? state?.api_key_id ?? 0;
    const selectedKey = keys.find(key => key.id === keyId);
    const allowed = selectedKey?.supported_models?.trim() ? new Set(selectedKey.supported_models.split(',').map(name => name.trim())) : undefined;
    const selectedGroup = groups.find(group => group.id === defaultGroupId);
    const customReasoning = !REASONING_EFFORTS.some(effort => effort === reasoning);
    const validKey = !!selectedKey?.enabled && (!selectedKey.expire_at || selectedKey.expire_at >= Date.now() / 1000);
    const routeGroupIds = routes.map(route => route.groupId).filter(groupId => groupId > 0);
    const validRoutes = routes.length > 0
        && routes.every(route => route.groupId > 0 && isDshAPI(route.api))
        && new Set(routeGroupIds).size === routeGroupIds.length
        && routes.some(route => route.groupId === defaultGroupId)
        && routes.every(route => {
            const group = groups.find(item => item.id === route.groupId);
            return !!group && (!allowed || allowed.has(group.name));
        });
    const validSelection = validKey && validRoutes;
    const fileDirty = !!file && draft !== undefined && content !== file.content;
    const routesChanged = routeSignature(routes) !== routeSignature(baselineRoutes);
    const quickPending = keyId !== (pendingCredential?.api_key_id ?? state?.api_key_id ?? 0)
        || routesChanged
        || defaultGroupId !== (pendingCredential?.default_model_group_id ?? baselineDefaultGroupId)
        || reasoning !== (pendingCredential?.reasoning_effort ?? savedReasoning);
    const busy = processing || createKey.isPending || autoConfig.isPending || saveConfig.isPending;
    const hasChanges = fileDirty || !!pendingCredential || quickPending;
    const canSave = !!file && !busy && !conflict && (view === 'file' ? fileDirty || !!pendingCredential : hasChanges && (!quickPending || validSelection));

    const resetQuick = () => {
        setKeyOverride(undefined);
        setRouteOverride(undefined);
        setDefaultGroupOverride(undefined);
        setReasoningOverride(undefined);
    };
    const putDraft = (text: string) => {
        if (file) setDraft(previous => ({ content: text, baseModifiedAt: previous?.baseModifiedAt ?? file.modified_at }));
    };

    const updateRoute = (key: string, patch: Partial<RouteDraft>) => {
        setRouteOverride(routes.map(route => route.key === key ? { ...route, ...patch } : route));
    };
    const addRoute = () => {
        const used = new Set(routes.map(route => route.groupId));
        const nextGroup = groups.find(group => !used.has(group.id) && (!allowed || allowed.has(group.name)));
        setRouteOverride([...routes, { key: `new:${Date.now()}:${Math.random()}`, groupId: nextGroup?.id ?? 0, api: 'openai-completions' }]);
    };
    const removeRoute = (key: string) => {
        const next = routes.filter(route => route.key !== key);
        setRouteOverride(next);
        if (!next.some(route => route.groupId === defaultGroupId)) setDefaultGroupOverride(next[0]?.groupId ?? 0);
    };

    const prepareDraft = async () => {
        if (!file || !validSelection) throw new Error(t('dshConfig.selectRequired'));
        const result = await autoConfig.mutateAsync({
            api_key_id: keyId,
            dsh: {
                profile: PROFILE,
                routes: routeSelections(routes),
                default_model_group_id: defaultGroupId,
                reasoning_effort: reasoningOverride,
            },
            current_content: content,
        });
        if (!result.dsh) throw new Error(t('autoConfigFailed'));
        const pending = { ...result.dsh, api_key_id: keyId };
        putDraft(result.content);
        setPendingCredential(pending);
        resetQuick();
        return { content: result.content, pending };
    };

    const changeView = async (next: View): Promise<boolean> => {
        if (next === view || busy) return false;
        setProcessing(true);
        try {
            if (next === 'file' && quickPending && validSelection && !conflict) await prepareDraft();
            setView(next);
            return true;
        } catch (error) {
            toast.error(messageOf(error, t('autoConfigFailed')));
            setView('file');
            return true;
        } finally { setProcessing(false); }
    };

    const handleSave = async () => {
        if (!file || !canSave) return;
        setProcessing(true);
        try {
            let savedContent = content;
            let pending = pendingCredential;
            const baseModifiedAt = draft?.baseModifiedAt ?? file.modified_at;
            if (view === 'quick' && quickPending) {
                const generated = await prepareDraft();
                savedContent = generated.content;
                pending = generated.pending;
            }
            await saveConfig.mutateAsync({
                files: [{ key: 'profile', content: savedContent, base_modified_at: baseModifiedAt }],
                dsh: pending ? {
                    profile: PROFILE,
                    api_key_id: pending.api_key_id,
                    routes: (pending.routes ?? []).flatMap(route => route.model_group_id ? [{ model_group_id: route.model_group_id, api: route.api }] : []),
                    default_model_group_id: pending.default_model_group_id,
                    credential_revision: pending.credential_revision,
                } : { profile: PROFILE },
            });
            const refreshed = await configQuery.refetch();
            if (!refreshed.error) { setDraft(undefined); setPendingCredential(undefined); resetQuick(); }
            toast.success(t('configSaved'));
        } catch (error) {
            if (typeof error === 'object' && error !== null && 'code' in error && error.code === 409) {
                setConflict(true);
                await configQuery.refetch();
                toast.error(t('dshConfig.conflict'));
            } else toast.error(messageOf(error, t('configSaveFailed')));
        } finally { setProcessing(false); }
    };

    const reload = async () => {
        setProcessing(true);
        try {
            const result = await configQuery.refetch();
            if (result.error) throw result.error;
            setDraft(undefined); setPendingCredential(undefined); setConflict(false); resetQuick();
        } catch (error) { toast.error(messageOf(error, t('configLoadFailed'))); }
        finally { setProcessing(false); }
    };

    const handleCreateKey = () => createKey.mutate({ name: 'dsh-auto', enabled: true }, {
        onSuccess: key => { setCreatedKey(key); setKeyOverride(key.id); toast.success(t('autoConfigKeyCreated')); },
        onError: error => toast.error(messageOf(error, t('autoConfigFailed'))),
    });

    return (
        <>
            <AgentConfigHeader agent="deepseek-harness" description={t('dshConfig.description')} />
            <Tabs value={view} onValueChange={next => { if (next === 'quick' || next === 'file') void changeView(next); }} className="min-h-0 flex-1 gap-0">
                <div className="flex shrink-0 items-center justify-between gap-3">
                    <TabsList aria-label={t('dshConfig.views')} className="rounded-xl" onKeyDown={event => {
                        if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
                        event.preventDefault();
                        const next = event.key === 'Home' ? 'quick' : event.key === 'End' ? 'file' : view === 'quick' ? 'file' : 'quick';
                        void changeView(next).then(changed => { if (changed) (next === 'quick' ? quickTabRef : fileTabRef).current?.focus(); });
                    }}>
                        <TabsTrigger ref={quickTabRef} value="quick" id={`${instanceId}-quick-tab`} aria-controls={`${instanceId}-quick-panel`} aria-selected={view === 'quick'} tabIndex={view === 'quick' ? 0 : -1} disabled={busy} className="gap-1.5 rounded-lg px-3"><SlidersHorizontal className="size-3.5" />{t('dshConfig.quickTab')}</TabsTrigger>
                        <TabsTrigger ref={fileTabRef} value="file" id={`${instanceId}-file-tab`} aria-controls={`${instanceId}-file-panel`} aria-selected={view === 'file'} tabIndex={view === 'file' ? 0 : -1} disabled={busy} className="gap-1.5 rounded-lg px-3"><FileCode2 className="size-3.5" />{t('dshConfig.fileTab')}</TabsTrigger>
                    </TabsList>
                    <span className="rounded-lg bg-muted px-2 py-1 text-xs text-muted-foreground">{t('dshConfig.desktop')}</span>
                </div>
                <motion.div layout="position" className="flex min-h-0 flex-1 flex-col pt-5">
                    {configQuery.isLoading && <div className="flex items-center justify-center gap-2 py-16 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />{t('configLoading')}</div>}
                    {configQuery.error && <p role="alert" className="mb-4 rounded-xl bg-destructive/10 p-3 text-sm text-destructive">{messageOf(configQuery.error, t('configLoadFailed'))}</p>}
                    {conflict && <div role="alert" className="mb-4 flex flex-wrap items-center gap-3 rounded-xl bg-destructive/10 p-3 text-sm text-destructive"><span className="min-w-0 flex-1">{t('dshConfig.conflict')}</span><Button variant="outline" size="sm" disabled={busy} onClick={() => void reload()}>{t('configReload')}</Button></div>}
                    {file && <AgentConfigPanels>
                        <AgentConfigPanel value="quick" id={`${instanceId}-quick-panel`} aria-labelledby={`${instanceId}-quick-tab`} className="space-y-5">
                            <section className="space-y-3">
                                <div className="space-y-1"><h3 className="flex items-center gap-2 text-sm font-medium"><KeyRound className="size-4 text-muted-foreground" />{t('dshConfig.connection')}</h3><p className="text-xs text-muted-foreground">{t('dshConfig.connectionHint')}</p></div>
                                <div className="flex flex-wrap items-end gap-2"><div className="min-w-40 flex-1 space-y-1.5"><label htmlFor={`${instanceId}-key`} className="text-xs text-muted-foreground">{t('dshConfig.apiKey')}</label><Select value={keyId ? String(keyId) : NONE} onValueChange={next => setKeyOverride(next === NONE ? 0 : Number(next))} disabled={busy || keysQuery.isLoading}>
                                    <SelectTrigger id={`${instanceId}-key`} className="w-full rounded-xl bg-background shadow-none"><SelectValue placeholder={t('autoConfigSelectKey')} /></SelectTrigger><SelectContent className="z-[70] rounded-xl"><SelectItem value={NONE}>{t('autoConfigSelectKey')}</SelectItem>{keys.map(key => <SelectItem key={key.id} value={String(key.id)} disabled={!key.enabled || (!!key.expire_at && key.expire_at < Date.now() / 1000)} className="rounded-lg">{key.name}{!key.enabled ? ` ${t('dshConfig.disabledKey')}` : ''}</SelectItem>)}</SelectContent>
                                </Select></div><Button variant="outline" className="h-9 rounded-xl" disabled={busy} onClick={handleCreateKey}>{createKey.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Plus className="size-4" />}{t('autoConfigCreateKey')}</Button></div>
                                <p className="text-xs text-muted-foreground">{state?.credential_configured ? t('dshConfig.credentialConfigured') : t('dshConfig.credentialMissing')}</p>
                                {keysQuery.error && <p role="alert" className="text-xs text-destructive">{t('dshConfig.keysFailed')}</p>}
                            </section>
                            <section className="space-y-3 border-t border-border/60 pt-4">
                                <div className="space-y-1"><h3 className="text-sm font-medium">{t('dshConfig.models')}</h3><p className="text-xs text-muted-foreground">{t('dshConfig.modelsHint')}</p></div>
                                <div className="space-y-2">
                                    {routes.length === 0 && <p className="rounded-xl border border-dashed border-border/70 px-3 py-3 text-xs text-muted-foreground">{t('dshConfig.noRoutes')}</p>}
                                    {routes.map((route, index) => (
                                        <div key={route.key} className="rounded-xl border border-border/70 bg-background/40 p-2.5">
                                            <div className="flex items-center justify-between gap-2 pb-2">
                                                <span className="text-xs text-muted-foreground">{t('dshConfig.route', { index: index + 1 })}</span>
                                                <Button variant="ghost" size="icon" className="size-7 text-muted-foreground hover:text-destructive" onClick={() => removeRoute(route.key)} disabled={busy} aria-label={t('dshConfig.removeRoute')}><Trash2 className="size-3.5" /></Button>
                                            </div>
                                            <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,14rem)]">
                                                <AgentModelGroupSelect id={`${instanceId}-route-${index}`} value={route.groupId} groups={groups} allowed={allowed} placeholder={t('dshConfig.selectModel')} disabled={busy || groupsQuery.isLoading} onChange={groupId => updateRoute(route.key, { groupId })} />
                                                <Select value={route.api} onValueChange={next => { if (isDshAPI(next)) updateRoute(route.key, { api: next }); }} disabled={busy}>
                                                    <SelectTrigger className="w-full rounded-xl bg-background shadow-none"><SelectValue /></SelectTrigger>
                                                    <SelectContent className="z-[70] rounded-xl">{APIS.map(item => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent>
                                                </Select>
                                            </div>
                                        </div>
                                    ))}
                                    <Button variant="outline" className="h-9 rounded-xl" disabled={busy || groups.length === 0 || routes.length >= groups.length} onClick={addRoute}><Plus className="size-4" />{t('dshConfig.addRoute')}</Button>
                                </div>
                                <div className="space-y-1.5"><label htmlFor={`${instanceId}-model`} className="text-xs text-muted-foreground">{t('dshConfig.defaultModel')}</label>
                                    <Select value={defaultGroupId ? String(defaultGroupId) : NONE} onValueChange={next => setDefaultGroupOverride(next === NONE ? 0 : Number(next))} disabled={busy || routes.length === 0}>
                                        <SelectTrigger id={`${instanceId}-model`} className="w-full rounded-xl bg-background shadow-none"><SelectValue placeholder={t('dshConfig.selectModel')} /></SelectTrigger>
                                        <SelectContent className="z-[70] rounded-xl">{routes.filter(route => route.groupId > 0).map(route => {
                                            const group = groups.find(item => item.id === route.groupId);
                                            return group ? <SelectItem key={route.key} value={String(route.groupId)} className="rounded-lg">{group.name}</SelectItem> : null;
                                        })}</SelectContent>
                                    </Select>
                                </div>
                                <div className="space-y-1.5">
                                    <label htmlFor={`${instanceId}-reasoning`} className="text-xs text-muted-foreground">{t('dshConfig.reasoningEffort')}</label>
                                    <Select value={reasoning} onValueChange={next => {
                                        const effort = REASONING_EFFORTS.find(value => value === next);
                                        if (effort) setReasoningOverride(effort);
                                    }} disabled={busy || !selectedGroup}>
                                        <SelectTrigger id={`${instanceId}-reasoning`} className="w-full rounded-xl bg-background shadow-none"><SelectValue /></SelectTrigger>
                                        <SelectContent className="z-[70] rounded-xl">
                                            {REASONING_EFFORTS.map(effort => <SelectItem key={effort} value={effort}>{t(`dshConfig.reasoningLevels.${effort}`)}</SelectItem>)}
                                            {customReasoning && <SelectItem value={reasoning} disabled>{t('dshConfig.reasoningExisting', { effort: reasoning })}</SelectItem>}
                                        </SelectContent>
                                    </Select>
                                    <p className="text-xs text-muted-foreground">{t('dshConfig.reasoningHint')}</p>
                                </div>
                                {state?.current_model && <p className="truncate text-xs text-muted-foreground">{t('dshConfig.currentModel', { provider: state.current_provider, model: state.current_model })}</p>}
                                {groupsQuery.error && <p role="alert" className="text-xs text-destructive">{t('dshConfig.groupsFailed')}</p>}
                                {!groupsQuery.isLoading && groups.length === 0 && <p className="text-xs text-muted-foreground">{t('dshConfig.noGroups')}</p>}
                                {pendingCredential?.reasoning_reset && <p className="text-xs text-muted-foreground">{t('dshConfig.reasoningReset')}</p>}
                            </section>
                            <section className="space-y-2 border-t border-border/60 pt-4">
                                <h3 className="text-sm font-medium">{t('dshConfig.advanced')}</h3>
                                <p className="text-xs text-muted-foreground">{t('dshConfig.protocolHint')}</p>
                            </section>
                        </AgentConfigPanel>
                        <AgentConfigPanel value="file" scrollable={false} id={`${instanceId}-file-panel`} aria-labelledby={`${instanceId}-file-tab`} className="flex flex-col gap-3 pb-1">
                            <div className="flex shrink-0 items-center justify-between gap-3"><p className="min-w-0 truncate font-mono text-xs text-muted-foreground" title={file.path}>{file.path}</p><span className="rounded-md bg-muted px-2 py-0.5 text-xs uppercase text-muted-foreground">YAML</span></div>
                            <AgentConfigTextEditor fileKey={`dsh-profile:${PROFILE}`} active={view === 'file'} label={t('dshConfig.sourceLabel')} value={content} disabled={busy} placeholder={t('configNewFilePlaceholder')} onChange={text => { putDraft(text); resetQuick(); }} />
                            <p className="shrink-0 text-xs text-muted-foreground">{pendingCredential ? t('dshConfig.privatePending') : t('dshConfig.fileHint')}</p>
                        </AgentConfigPanel>
                    </AgentConfigPanels>}
                </motion.div>
            </Tabs>
            <footer className="mt-5 flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-border/60 pt-4"><div className="min-w-0 space-y-1"><p className="flex items-center gap-1.5 text-xs text-muted-foreground">{hasChanges ? <span className="size-1.5 rounded-full bg-amber-500" /> : <Check className="size-3" />}{hasChanges ? t('configUnsaved') : t('configInSync')}<span className="ml-1 font-mono">cordis.patch.yml</span></p><p className="text-xs text-muted-foreground">{t('dshConfig.saveHint')}</p></div><Button className="min-w-28 rounded-xl" disabled={!canSave} onClick={() => void handleSave()}>{processing || saveConfig.isPending ? <LoaderCircle className="size-4 animate-spin" /> : null}{t('dshConfig.save')}</Button></footer>
        </>
    );
}
