'use client';

import { useId, useRef, useState } from 'react';
import { motion } from 'motion/react';
import { useTranslations } from 'next-intl';
import { Check, ChevronDown, FileCode2, KeyRound, LoaderCircle, Plus, SlidersHorizontal } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsList, TabsTrigger } from '@/components/animate-ui/components/animate/tabs';
import { toast } from '@/components/common/Toast';
import { useAPIKeyList, useCreateAPIKey, type APIKey } from '@/api/endpoints/apikey';
import { useGroupList } from '@/api/endpoints/group';
import { useAgentAutoConfig, useAgentConfig, useSaveAgentConfig, type ClaudeCodeModelGroups } from '@/api/endpoints/agent-config';
import { cn } from '@/lib/utils';
import { AgentConfigHeader } from './AgentConfigHeader';
import { AgentConfigPanel, AgentConfigPanels } from './AgentConfigPanels';
import { AgentConfigTextEditor } from './AgentConfigTextEditor';
import { AgentModelGroupSelect as ModelGroupSelect, type AgentModelGroup as ModelGroup } from './AgentModelGroupSelect';

const NONE = '__keep__';
type View = 'quick' | 'file';
type ModelRole = Exclude<keyof ClaudeCodeModelGroups, 'default'>;
type Draft = { content: string; baseModifiedAt: number };

const MODEL_ROLES: { role: ModelRole; label: string; env: string }[] = [
    { role: 'opus', label: 'Opus', env: 'ANTHROPIC_DEFAULT_OPUS_MODEL' },
    { role: 'sonnet', label: 'Sonnet', env: 'ANTHROPIC_DEFAULT_SONNET_MODEL' },
    { role: 'haiku', label: 'Haiku', env: 'ANTHROPIC_DEFAULT_HAIKU_MODEL' },
    { role: 'fable', label: 'Fable', env: 'ANTHROPIC_DEFAULT_FABLE_MODEL' },
    { role: 'subagent', label: 'subagent', env: 'CLAUDE_CODE_SUBAGENT_MODEL' },
];

function isObject(value: unknown): value is Record<string, unknown> {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function parseSettings(content: string): { root: Record<string, unknown>; env: Record<string, unknown>; invalid: boolean } {
    try {
        const value: unknown = content.trim() ? JSON.parse(content) : {};
        if (!isObject(value)) return { root: {}, env: {}, invalid: true };
        if (value.env != null && !isObject(value.env)) return { root: value, env: {}, invalid: true };
        return { root: value, env: isObject(value.env) ? value.env : {}, invalid: false };
    } catch {
        return { root: {}, env: {}, invalid: true };
    }
}

function stringValue(value: unknown): string {
    return typeof value === 'string' ? value : '';
}

function messageFor(error: unknown, fallback: string): string {
    return isObject(error) && typeof error.message === 'string' ? error.message : fallback;
}

export function ClaudeCodeConfigContent() {
    const t = useTranslations('topology');
    const instanceId = useId();
    const quickTabRef = useRef<HTMLButtonElement>(null);
    const fileTabRef = useRef<HTMLButtonElement>(null);
    const configQuery = useAgentConfig('claude-code');
    const apiKeysQuery = useAPIKeyList();
    const groupsQuery = useGroupList();
    const createKey = useCreateAPIKey();
    const autoConfig = useAgentAutoConfig('claude-code');
    const saveConfig = useSaveAgentConfig('claude-code');
    const [view, setView] = useState<View>('quick');
    const [draft, setDraft] = useState<Draft>();
    const [keyOverride, setKeyOverride] = useState<number>();
    const [createdKey, setCreatedKey] = useState<APIKey>();
    const [models, setModels] = useState<ClaudeCodeModelGroups>({});
    const [rolesExpanded, setRolesExpanded] = useState(false);
    const [processing, setProcessing] = useState(false);
    const [conflict, setConflict] = useState(false);

    const file = configQuery.data?.files.find((item) => item.key === 'settings');
    const content = draft?.content ?? file?.content ?? '';
    const settings = parseSettings(content);
    const keys = [...(apiKeysQuery.data ?? [])];
    if (createdKey && !keys.some((key) => key.id === createdKey.id)) keys.push(createdKey);
    const currentToken = stringValue(settings.env.ANTHROPIC_AUTH_TOKEN) || stringValue(settings.env.ANTHROPIC_API_KEY);
    const currentKey = keys.find((key) => key.api_key === currentToken);
    const keyId = keyOverride ?? currentKey?.id ?? 0;
    const selectedKey = keys.find((key) => key.id === keyId);
    const validKey = !!selectedKey?.enabled;
    const allowed = selectedKey?.supported_models?.trim()
        ? new Set(selectedKey.supported_models.split(',').map((name) => name.trim()))
        : undefined;
    const groups = (groupsQuery.data ?? [])
        .filter((group): group is ModelGroup => typeof group.id === 'number' && group.id > 0)
        .sort((a, b) => a.name.localeCompare(b.name));
    const hasModels = Object.values(models).some((id) => typeof id === 'number' && id > 0);
    const quickPending = (keyOverride !== undefined && keyOverride !== currentKey?.id) || hasModels;
    const fileDirty = draft !== undefined && draft.content !== (file?.content ?? '');
    const busy = processing || autoConfig.isPending || saveConfig.isPending || createKey.isPending;
    const currentModel = stringValue(settings.env.ANTHROPIC_MODEL) || stringValue(settings.root.model) || stringValue(settings.env.ANTHROPIC_DEFAULT_SONNET_MODEL);
    const hasChanges = quickPending || fileDirty;
    const canSave = !!file && !busy && !conflict && (view === 'file' ? fileDirty : hasChanges && (!quickPending || validKey));

    const resetQuick = () => {
        setKeyOverride(undefined);
        setModels({});
    };

    const chooseDefault = (id: number) => {
        // 选择默认分组时统一各用途；展开后的单项选择可以覆盖这一分配。
        setModels(id > 0 ? { default: id, opus: id, sonnet: id, haiku: id, fable: id, subagent: id } : {});
    };

    const chooseRole = (role: ModelRole, id: number) => {
        setModels((previous) => {
            const next = { ...previous };
            if (id > 0) next[role] = id;
            else delete next[role];
            return next;
        });
    };

    const prepareDraft = async (): Promise<string> => {
        if (!file) throw new Error(t('configLoadFailed'));
        if (!validKey) throw new Error(t('claudeConfig.selectKeyError'));
        const result = await autoConfig.mutateAsync({
            api_key_id: keyId,
            model_groups: hasModels ? models : undefined,
            current_content: content,
        });
        setDraft({ content: result.content, baseModifiedAt: draft?.baseModifiedAt ?? file.modified_at });
        resetQuick();
        return result.content;
    };

    const changeView = async (next: View): Promise<boolean> => {
        if (next === view || busy) return false;
        setProcessing(true);
        try {
            // 源码页展示快捷配置的实际结果；手工修改源码后，以手工草稿为准。
            if (next === 'file' && quickPending && validKey && !settings.invalid && !conflict) await prepareDraft();
            setView(next);
            return true;
        } catch (error) {
            toast.error(messageFor(error, t('autoConfigFailed')));
            return false;
        } finally {
            setProcessing(false);
        }
    };

    const handleSave = async () => {
        if (!file || !canSave) return;
        setProcessing(true);
        try {
            const baseModifiedAt = draft?.baseModifiedAt ?? file.modified_at;
            const nextContent = view === 'quick' && quickPending ? await prepareDraft() : content;
            await saveConfig.mutateAsync([{ key: file.key, content: nextContent, base_modified_at: baseModifiedAt }]);
            const refreshed = await configQuery.refetch();
            if (!refreshed.error) setDraft(undefined);
            resetQuick();
            toast.success(t('configSaved'));
        } catch (error) {
            if (isObject(error) && error.code === 409) {
                // 保留生成草稿的原始时间戳，防止刷新后把外部修改直接覆盖。
                setConflict(true);
                await configQuery.refetch();
                toast.error(t('claudeConfig.conflict'));
            } else {
                toast.error(messageFor(error, t('configSaveFailed')));
            }
        } finally {
            setProcessing(false);
        }
    };

    const reload = async () => {
        setProcessing(true);
        try {
            const refreshed = await configQuery.refetch();
            if (refreshed.error) throw refreshed.error;
            setDraft(undefined);
            setConflict(false);
            resetQuick();
        } catch (error) {
            toast.error(messageFor(error, t('configLoadFailed')));
        } finally {
            setProcessing(false);
        }
    };

    const handleCreateKey = () => {
        createKey.mutate({ name: 'claude-code-auto', enabled: true }, {
            onSuccess: (key) => {
                setCreatedKey(key);
                setKeyOverride(key.id);
                toast.success(t('autoConfigKeyCreated'));
            },
            onError: (error) => toast.error(messageFor(error, t('autoConfigFailed'))),
        });
    };

    return (
        <>
            <AgentConfigHeader agent="claude-code" description={t('claudeConfig.description')} />
            <Tabs value={view} onValueChange={(next) => { if (next === 'quick' || next === 'file') void changeView(next); }} className="min-h-0 flex-1 gap-0">
                <TabsList aria-label={t('claudeConfig.views')} className="shrink-0 rounded-xl" onKeyDown={(event) => {
                    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
                    event.preventDefault();
                    const next = event.key === 'Home' ? 'quick' : event.key === 'End' ? 'file' : view === 'quick' ? 'file' : 'quick';
                    void changeView(next).then((changed) => { if (changed) (next === 'quick' ? quickTabRef : fileTabRef).current?.focus(); });
                }}>
                    <TabsTrigger ref={quickTabRef} value="quick" id={`${instanceId}-quick-tab`} aria-controls={`${instanceId}-quick-panel`} aria-selected={view === 'quick'} tabIndex={view === 'quick' ? 0 : -1} disabled={busy} className="gap-1.5 rounded-lg px-3">
                        <SlidersHorizontal className="size-3.5" />{t('claudeConfig.quickTab')}
                    </TabsTrigger>
                    <TabsTrigger ref={fileTabRef} value="file" id={`${instanceId}-file-tab`} aria-controls={`${instanceId}-file-panel`} aria-selected={view === 'file'} tabIndex={view === 'file' ? 0 : -1} disabled={busy} className="gap-1.5 rounded-lg px-3">
                        <FileCode2 className="size-3.5" />{t('claudeConfig.fileTab')}
                    </TabsTrigger>
                </TabsList>

                <motion.div layout="position" className="flex min-h-0 flex-1 flex-col pt-5">
                    {configQuery.isLoading && <div className="flex items-center justify-center gap-2 py-16 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />{t('configLoading')}</div>}
                    {configQuery.error && <p role="alert" className="mb-4 rounded-xl bg-destructive/10 p-3 text-sm text-destructive">{t('configLoadFailed')}</p>}
                    {conflict && <div role="alert" className="mb-4 flex flex-wrap items-center gap-3 rounded-xl bg-destructive/10 p-3 text-sm text-destructive"><span className="min-w-0 flex-1">{t('claudeConfig.conflict')}</span><Button variant="outline" size="sm" disabled={busy} onClick={() => void reload()}>{t('configReload')}</Button></div>}
                    {file && (
                        <AgentConfigPanels>
                            <AgentConfigPanel value="quick" id={`${instanceId}-quick-panel`} aria-labelledby={`${instanceId}-quick-tab`} className="space-y-6">
                                {settings.invalid && <p role="alert" className="rounded-xl bg-destructive/10 p-3 text-sm text-destructive">{t('claudeConfig.invalidFile')}</p>}
                                <section className="space-y-3">
                                    <div className="space-y-1"><h3 className="flex items-center gap-2 text-sm font-medium"><KeyRound className="size-4 text-muted-foreground" />{t('claudeConfig.connection')}</h3><p className="text-xs text-muted-foreground">{t('claudeConfig.connectionHint')}</p></div>
                                    <div className="flex flex-wrap items-end gap-2">
                                        <div className="min-w-40 flex-1 space-y-1.5"><label htmlFor={`${instanceId}-key`} className="text-xs text-muted-foreground">{t('claudeConfig.apiKey')}</label>
                                            <Select value={keyId ? String(keyId) : NONE} onValueChange={(next) => setKeyOverride(next === NONE ? 0 : Number(next))} disabled={busy || apiKeysQuery.isLoading}>
                                                <SelectTrigger id={`${instanceId}-key`} className="w-full rounded-xl bg-background shadow-none"><SelectValue placeholder={t('autoConfigSelectKey')} /></SelectTrigger>
                                                <SelectContent className="z-[70] rounded-xl"><SelectItem value={NONE}>{t('autoConfigSelectKey')}</SelectItem>{keys.map((key) => <SelectItem key={key.id} value={String(key.id)} disabled={!key.enabled} className="rounded-lg">{key.name}{!key.enabled ? ` ${t('claudeConfig.disabledKey')}` : ''}</SelectItem>)}</SelectContent>
                                            </Select>
                                        </div>
                                        <Button variant="outline" className="h-9 rounded-xl" disabled={busy} onClick={handleCreateKey}>{createKey.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Plus className="size-4" />}{t('autoConfigCreateKey')}</Button>
                                    </div>
                                    {apiKeysQuery.error && <p role="alert" className="text-xs text-destructive">{t('claudeConfig.keysFailed')}</p>}
                                </section>

                                <section className="space-y-3 border-t border-border/60 pt-5">
                                    <div className="space-y-1"><h3 className="text-sm font-medium">{t('claudeConfig.models')}</h3><p className="text-xs text-muted-foreground">{t('claudeConfig.modelsHint')}</p></div>
                                    <div className="space-y-1.5"><label htmlFor={`${instanceId}-model`} className="text-xs text-muted-foreground">{t('claudeConfig.defaultModel')}</label><ModelGroupSelect id={`${instanceId}-model`} value={models.default} groups={groups} allowed={allowed} placeholder={t('claudeConfig.keepModels')} disabled={busy || groupsQuery.isLoading} onChange={chooseDefault} /></div>
                                    {currentModel && <p className="truncate text-xs text-muted-foreground" title={currentModel}>{t('claudeConfig.currentModel', { model: currentModel })}</p>}
                                    {groupsQuery.error && <p role="alert" className="text-xs text-destructive">{t('claudeConfig.groupsFailed')}</p>}
                                    {!groupsQuery.isLoading && !groupsQuery.error && groups.length === 0 && <p className="text-xs text-muted-foreground">{t('claudeConfig.noGroups')}</p>}
                                    {allowed && <p className="text-xs text-muted-foreground">{t('claudeConfig.restrictedModels')}</p>}

                                    <button type="button" aria-expanded={rolesExpanded} aria-controls={`${instanceId}-roles`} onClick={() => setRolesExpanded((expanded) => !expanded)} className="flex w-full items-center justify-between gap-3 rounded-lg py-2 text-xs text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                                        <span>{t('claudeConfig.roleModels')}</span><ChevronDown className={cn('size-4 transition-transform', rolesExpanded && 'rotate-180')} />
                                    </button>
                                    {rolesExpanded && <div id={`${instanceId}-roles`} className="grid gap-x-4 gap-y-4 sm:grid-cols-2">
                                        {MODEL_ROLES.map(({ role, label, env }) => {
                                            const existingModel = stringValue(settings.env[env]);
                                            return <div key={role} className="min-w-0 space-y-1.5"><label htmlFor={`${instanceId}-${role}`} className="text-xs text-muted-foreground">{role === 'subagent' ? t('claudeConfig.subagent') : label}</label><ModelGroupSelect id={`${instanceId}-${role}`} value={models[role]} groups={groups} allowed={allowed} placeholder={t('claudeConfig.keepRole')} disabled={busy || groupsQuery.isLoading} onChange={(id) => chooseRole(role, id)} />{existingModel && <p className="truncate text-xs text-muted-foreground" title={existingModel}>{t('claudeConfig.currentModel', { model: existingModel })}</p>}</div>;
                                        })}
                                    </div>}
                                </section>
                            </AgentConfigPanel>
                            <AgentConfigPanel value="file" scrollable={false} id={`${instanceId}-file-panel`} aria-labelledby={`${instanceId}-file-tab`} className="flex flex-col gap-3 pb-1">
                                <div className="flex shrink-0 items-center justify-between gap-3"><p className="min-w-0 truncate font-mono text-xs text-muted-foreground" title={file.path}>{file.path}</p><span className="rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground">JSON</span></div>
                                <AgentConfigTextEditor
                                    fileKey={file.key}
                                    active={view === 'file'}
                                    label={t('claudeConfig.sourceLabel')}
                                    value={content}
                                    onChange={(nextContent) => { setDraft({ content: nextContent, baseModifiedAt: draft?.baseModifiedAt ?? file.modified_at }); resetQuick(); }}
                                    disabled={busy}
                                    placeholder={t('configNewFilePlaceholder')}
                                />
                            </AgentConfigPanel>
                        </AgentConfigPanels>
                    )}
                </motion.div>
            </Tabs>

            <footer className="mt-5 flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-border/60 pt-4">
                <div className="min-w-0 space-y-1"><p className="flex items-center gap-1.5 text-xs text-muted-foreground">{hasChanges ? <span className="size-1.5 rounded-full bg-amber-500" /> : <Check className="size-3" />}{hasChanges ? t('configUnsaved') : t('configInSync')}<span className="ml-1 font-mono" title={file?.path}>settings.json</span></p><p className="text-xs text-muted-foreground">{t('claudeConfig.saveHint')}</p></div>
                <Button className="min-w-28 rounded-xl" disabled={!canSave} onClick={() => void handleSave()}>{processing || saveConfig.isPending ? <LoaderCircle className="size-4 animate-spin" /> : null}{t('claudeConfig.save')}</Button>
            </footer>
        </>
    );
}
