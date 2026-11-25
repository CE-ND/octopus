'use client';

import { useId, useRef, useState } from 'react';
import { motion } from 'motion/react';
import { useTranslations } from 'next-intl';
import { Check, FileCode2, KeyRound, LoaderCircle, Plus, SlidersHorizontal } from 'lucide-react';
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
    type AgentConfigFileView,
    type AgentConfigSaveFile,
} from '@/api/endpoints/agent-config';
import { AgentConfigHeader } from './AgentConfigHeader';
import { AgentConfigPanel, AgentConfigPanels } from './AgentConfigPanels';
import { AgentConfigTextEditor } from './AgentConfigTextEditor';
import { AgentModelGroupSelect, type AgentModelGroup } from './AgentModelGroupSelect';

const NONE = '__keep__';
type View = 'quick' | 'file';
type Draft = { content: string; baseModifiedAt: number };

/** 读取 .env 中的一个变量；支持 `export KEY=value`、单双引号和行尾注释。 */
function dotenvValue(content: string, key: string): string {
    for (const line of content.split(/\r?\n/)) {
        const trimmed = line.trim();
        if (!trimmed || trimmed.startsWith('#')) continue;
        const body = trimmed.startsWith('export ') ? trimmed.slice('export '.length).trim() : trimmed;
        const eq = body.indexOf('=');
        if (eq <= 0 || body.slice(0, eq).trim() !== key) continue;
        let value = body.slice(eq + 1).trim();
        if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
            value = value.slice(1, -1);
        } else {
            const comment = value.search(/\s#/);
            if (comment >= 0) value = value.slice(0, comment).trim();
        }
        return value;
    }
    return '';
}

function settingsModelName(content: string): string {
    try {
        const value: unknown = content.trim() ? JSON.parse(content) : {};
        if (typeof value !== 'object' || value === null) return '';
        const model = (value as { model?: unknown }).model;
        if (typeof model !== 'object' || model === null) return '';
        const name = (model as { name?: unknown }).name;
        return typeof name === 'string' ? name : '';
    } catch {
        return '';
    }
}

function messageFor(error: unknown, fallback: string): string {
    return error instanceof Error ? error.message
        : typeof error === 'object' && error !== null && 'message' in error && typeof error.message === 'string'
            ? error.message
            : fallback;
}

export function GeminiCliConfigContent() {
    const t = useTranslations('topology');
    const instanceId = useId();
    const quickTabRef = useRef<HTMLButtonElement>(null);
    const fileTabRef = useRef<HTMLButtonElement>(null);
    const configQuery = useAgentConfig('gemini-cli');
    const keysQuery = useAPIKeyList();
    const groupsQuery = useGroupList();
    const createKey = useCreateAPIKey();
    const autoConfig = useAgentAutoConfig('gemini-cli');
    const saveConfig = useSaveAgentConfig('gemini-cli');
    const [view, setView] = useState<View>('quick');
    const [activeFileKey, setActiveFileKey] = useState('env');
    const [drafts, setDrafts] = useState<Record<string, Draft>>({});
    const [keyOverride, setKeyOverride] = useState<number>();
    const [createdKey, setCreatedKey] = useState<APIKey>();
    const [modelGroupId, setModelGroupId] = useState(0);
    const [processing, setProcessing] = useState(false);
    const [conflict, setConflict] = useState(false);

    const files = configQuery.data?.files ?? [];
    const envFile = files.find((file) => file.key === 'env');
    const settingsFile = files.find((file) => file.key === 'settings');
    const activeFile = files.find((file) => file.key === activeFileKey) ?? envFile ?? settingsFile;
    const contentFor = (file: AgentConfigFileView) => drafts[file.key]?.content ?? file.content;
    const envContent = envFile ? contentFor(envFile) : '';
    const settingsContent = settingsFile ? contentFor(settingsFile) : '';
    const currentKeyValue = dotenvValue(envContent, 'GEMINI_API_KEY');
    const currentModel = dotenvValue(envContent, 'GEMINI_MODEL') || settingsModelName(settingsContent);

    const keys = [...(keysQuery.data ?? [])];
    if (createdKey && !keys.some((key) => key.id === createdKey.id)) keys.push(createdKey);
    const currentKey = keys.find((key) => key.api_key === currentKeyValue);
    const keyId = keyOverride ?? currentKey?.id ?? 0;
    const selectedKey = keys.find((key) => key.id === keyId);
    const validKey = !!selectedKey?.enabled;
    const allowed = selectedKey?.supported_models?.trim()
        ? new Set(selectedKey.supported_models.split(',').map((name) => name.trim()))
        : undefined;
    const groups = (groupsQuery.data ?? [])
        .filter((group): group is AgentModelGroup => typeof group.id === 'number' && group.id > 0)
        .sort((a, b) => a.name.localeCompare(b.name));
    const savedGroup = groups.find((group) => group.name === currentModel);
    const selectedGroupId = modelGroupId || savedGroup?.id || 0;
    const selectedGroup = groups.find((group) => group.id === selectedGroupId);
    const modelReady = !selectedGroup || !allowed || allowed.has(selectedGroup.name);
    const quickPending = (keyOverride !== undefined && keyOverride !== currentKey?.id) || modelGroupId > 0;
    const dirtyFiles = files.filter((file) => drafts[file.key] !== undefined && contentFor(file) !== file.content);
    const busy = processing || autoConfig.isPending || saveConfig.isPending || createKey.isPending;
    const hasChanges = quickPending || dirtyFiles.length > 0;
    const canSave = !!envFile && !!settingsFile && !busy && !conflict
        && (view === 'file' ? dirtyFiles.length > 0 : hasChanges && (!quickPending || (validKey && modelReady)));
    const ready = !!envFile && !!settingsFile;

    const resetQuick = () => {
        setKeyOverride(undefined);
        setModelGroupId(0);
    };

    const putDraft = (file: AgentConfigFileView, content: string) => {
        setDrafts((previous) => ({
            ...previous,
            [file.key]: { content, baseModifiedAt: previous[file.key]?.baseModifiedAt ?? file.modified_at },
        }));
    };

    const applyResult = (result: { files?: { file_key: string; content: string }[]; file_key: string; content: string }) => {
        const entries = result.files?.length ? result.files : [{ file_key: result.file_key, content: result.content }];
        const next = { ...drafts };
        for (const entry of entries) {
            const file = files.find((item) => item.key === entry.file_key);
            if (!file) continue;
            next[file.key] = { content: entry.content, baseModifiedAt: drafts[file.key]?.baseModifiedAt ?? file.modified_at };
        }
        setDrafts(next);
        return next;
    };

    const prepareDraft = async () => {
        if (!ready) throw new Error(t('configLoadFailed'));
        if (!validKey) throw new Error(t('geminiCliConfig.selectKeyError'));
        const result = await autoConfig.mutateAsync({
            api_key_id: keyId,
            gemini_model_group_id: selectedGroupId || undefined,
            current_contents: { env: envContent, settings: settingsContent },
        });
        const next = applyResult(result);
        resetQuick();
        return next;
    };

    const changeView = async (next: View): Promise<boolean> => {
        if (next === view || busy) return false;
        setProcessing(true);
        try {
            if (next === 'file' && quickPending && validKey && !conflict) await prepareDraft();
            setView(next);
            return true;
        } catch (error) {
            toast.error(messageFor(error, t('autoConfigFailed')));
            setView('file');
            return true;
        } finally {
            setProcessing(false);
        }
    };

    const handleSave = async () => {
        if (!ready || !canSave) return;
        setProcessing(true);
        try {
            let nextDrafts = drafts;
            if (view === 'quick' && quickPending) nextDrafts = await prepareDraft();
            const inputs: AgentConfigSaveFile[] = files
                .filter((file) => nextDrafts[file.key] !== undefined && nextDrafts[file.key].content !== file.content)
                .map((file) => ({ key: file.key, content: nextDrafts[file.key].content, base_modified_at: nextDrafts[file.key].baseModifiedAt }));
            if (inputs.length === 0) return;
            await saveConfig.mutateAsync(inputs);
            const refreshed = await configQuery.refetch();
            if (!refreshed.error) setDrafts({});
            resetQuick();
            toast.success(t('configSaved'));
        } catch (error) {
            if (typeof error === 'object' && error !== null && 'code' in error && error.code === 409) {
                setConflict(true);
                await configQuery.refetch();
                toast.error(t('geminiCliConfig.conflict'));
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
            setDrafts({});
            setConflict(false);
            resetQuick();
        } catch (error) {
            toast.error(messageFor(error, t('configLoadFailed')));
        } finally {
            setProcessing(false);
        }
    };

    const handleCreateKey = () => {
        createKey.mutate({ name: 'gemini-cli-auto', enabled: true }, {
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
            <AgentConfigHeader agent="gemini-cli" description={t('geminiCliConfig.description')} />
            <Tabs value={view} onValueChange={(next) => { if (next === 'quick' || next === 'file') void changeView(next); }} className="min-h-0 flex-1 gap-0">
                <TabsList aria-label={t('geminiCliConfig.views')} className="shrink-0 rounded-xl" onKeyDown={(event) => {
                    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
                    event.preventDefault();
                    const next = event.key === 'Home' ? 'quick' : event.key === 'End' ? 'file' : view === 'quick' ? 'file' : 'quick';
                    void changeView(next).then((changed) => { if (changed) (next === 'quick' ? quickTabRef : fileTabRef).current?.focus(); });
                }}>
                    <TabsTrigger ref={quickTabRef} value="quick" id={`${instanceId}-quick-tab`} aria-controls={`${instanceId}-quick-panel`} aria-selected={view === 'quick'} tabIndex={view === 'quick' ? 0 : -1} disabled={busy} className="gap-1.5 rounded-lg px-3">
                        <SlidersHorizontal className="size-3.5" />{t('geminiCliConfig.quickTab')}
                    </TabsTrigger>
                    <TabsTrigger ref={fileTabRef} value="file" id={`${instanceId}-file-tab`} aria-controls={`${instanceId}-file-panel`} aria-selected={view === 'file'} tabIndex={view === 'file' ? 0 : -1} disabled={busy} className="gap-1.5 rounded-lg px-3">
                        <FileCode2 className="size-3.5" />{t('geminiCliConfig.fileTab')}
                    </TabsTrigger>
                </TabsList>

                <motion.div layout="position" className="flex min-h-0 flex-1 flex-col pt-5">
                    {configQuery.isLoading && <div className="flex items-center justify-center gap-2 py-16 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />{t('configLoading')}</div>}
                    {configQuery.error && <p role="alert" className="mb-4 rounded-xl bg-destructive/10 p-3 text-sm text-destructive">{t('configLoadFailed')}</p>}
                    {conflict && <div role="alert" className="mb-4 flex flex-wrap items-center gap-3 rounded-xl bg-destructive/10 p-3 text-sm text-destructive"><span className="min-w-0 flex-1">{t('geminiCliConfig.conflict')}</span><Button variant="outline" size="sm" disabled={busy} onClick={() => void reload()}>{t('configReload')}</Button></div>}
                    {ready && (
                        <AgentConfigPanels>
                            <AgentConfigPanel value="quick" id={`${instanceId}-quick-panel`} aria-labelledby={`${instanceId}-quick-tab`} className="space-y-6">
                                <section className="space-y-3">
                                    <div className="space-y-1"><h3 className="flex items-center gap-2 text-sm font-medium"><KeyRound className="size-4 text-muted-foreground" />{t('geminiCliConfig.connection')}</h3><p className="text-xs text-muted-foreground">{t('geminiCliConfig.connectionHint')}</p></div>
                                    <div className="flex flex-wrap items-end gap-2">
                                        <div className="min-w-40 flex-1 space-y-1.5"><label htmlFor={`${instanceId}-key`} className="text-xs text-muted-foreground">{t('geminiCliConfig.apiKey')}</label>
                                            <Select value={keyId ? String(keyId) : NONE} onValueChange={(next) => setKeyOverride(next === NONE ? 0 : Number(next))} disabled={busy || keysQuery.isLoading}>
                                                <SelectTrigger id={`${instanceId}-key`} className="w-full rounded-xl bg-background shadow-none"><SelectValue placeholder={t('autoConfigSelectKey')} /></SelectTrigger>
                                                <SelectContent className="z-[70] rounded-xl"><SelectItem value={NONE}>{t('autoConfigSelectKey')}</SelectItem>{keys.map((key) => <SelectItem key={key.id} value={String(key.id)} disabled={!key.enabled} className="rounded-lg">{key.name}{!key.enabled ? ` ${t('geminiCliConfig.disabledKey')}` : ''}</SelectItem>)}</SelectContent>
                                            </Select>
                                        </div>
                                        <Button variant="outline" className="h-9 rounded-xl" disabled={busy} onClick={handleCreateKey}>{createKey.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Plus className="size-4" />}{t('autoConfigCreateKey')}</Button>
                                    </div>
                                    {keysQuery.error && <p role="alert" className="text-xs text-destructive">{t('geminiCliConfig.keysFailed')}</p>}
                                </section>

                                <section className="space-y-3 border-t border-border/60 pt-5">
                                    <div className="space-y-1"><h3 className="text-sm font-medium">{t('geminiCliConfig.models')}</h3><p className="text-xs text-muted-foreground">{t('geminiCliConfig.modelsHint')}</p></div>
                                    <div className="space-y-1.5"><label htmlFor={`${instanceId}-model`} className="text-xs text-muted-foreground">{t('geminiCliConfig.defaultModel')}</label><AgentModelGroupSelect id={`${instanceId}-model`} value={selectedGroupId} groups={groups} allowed={allowed} placeholder={t('geminiCliConfig.keepModels')} disabled={busy || groupsQuery.isLoading} onChange={setModelGroupId} /></div>
                                    {currentModel && <p className="truncate text-xs text-muted-foreground" title={currentModel}>{t('geminiCliConfig.currentModel', { model: currentModel })}</p>}
                                    {groupsQuery.error && <p role="alert" className="text-xs text-destructive">{t('geminiCliConfig.groupsFailed')}</p>}
                                    {!groupsQuery.isLoading && !groupsQuery.error && groups.length === 0 && <p className="text-xs text-muted-foreground">{t('geminiCliConfig.noGroups')}</p>}
                                    {allowed && <p className="text-xs text-muted-foreground">{t('geminiCliConfig.restrictedModels')}</p>}
                                    <p className="text-xs text-muted-foreground">{t('geminiCliConfig.envHint')}</p>
                                </section>
                            </AgentConfigPanel>

                            {activeFile && <AgentConfigPanel value="file" scrollable={false} id={`${instanceId}-file-panel`} aria-labelledby={`${instanceId}-file-tab`} className="flex flex-col gap-3 pb-1">
                                <div className="flex shrink-0 flex-wrap items-center justify-between gap-3"><Select value={activeFile.key} onValueChange={setActiveFileKey} disabled={busy}><SelectTrigger aria-label={t('geminiCliConfig.selectFile')} className="rounded-xl bg-background shadow-none"><SelectValue /></SelectTrigger><SelectContent className="z-[70] rounded-xl">{files.map((file) => <SelectItem key={file.key} value={file.key} className="rounded-lg">{file.key === 'env' ? '.env' : 'settings.json'}{drafts[file.key] && contentFor(file) !== file.content ? ` · ${t('configUnsaved')}` : ''}</SelectItem>)}</SelectContent></Select><span className="rounded-md bg-muted px-2 py-0.5 text-xs uppercase text-muted-foreground">{activeFile.language}</span></div>
                                <p className="shrink-0 truncate font-mono text-xs text-muted-foreground" title={activeFile.path}>{activeFile.path}</p>
                                <AgentConfigTextEditor
                                    fileKey={activeFile.key}
                                    active={view === 'file'}
                                    label={t('geminiCliConfig.sourceLabel', { file: activeFile.key === 'env' ? '.env' : 'settings.json' })}
                                    value={contentFor(activeFile)}
                                    onChange={(content) => { putDraft(activeFile, content); resetQuick(); }}
                                    disabled={busy}
                                    placeholder={t('configNewFilePlaceholder')}
                                />
                            </AgentConfigPanel>}
                        </AgentConfigPanels>
                    )}
                </motion.div>
            </Tabs>

            <footer className="mt-5 flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-border/60 pt-4">
                <div className="min-w-0 space-y-1"><p className="flex items-center gap-1.5 text-xs text-muted-foreground">{hasChanges ? <span className="size-1.5 rounded-full bg-amber-500" /> : <Check className="size-3" />}{hasChanges ? t('configUnsaved') : t('configInSync')}<span className="ml-1 font-mono">{dirtyFiles.length > 1 ? t('geminiCliConfig.fileCount', { count: dirtyFiles.length }) : activeFile?.key === 'settings' && view === 'file' ? 'settings.json' : '.env'}</span></p><p className="text-xs text-muted-foreground">{t('geminiCliConfig.saveHint')}</p></div>
                <Button className="min-w-28 rounded-xl" disabled={!canSave} onClick={() => void handleSave()}>{processing || saveConfig.isPending ? <LoaderCircle className="size-4 animate-spin" /> : null}{t('geminiCliConfig.save')}</Button>
            </footer>
        </>
    );
}
