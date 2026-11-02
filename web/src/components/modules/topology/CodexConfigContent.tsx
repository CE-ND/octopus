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
import { useAgentAutoConfig, useAgentConfig, useSaveAgentConfig, type AgentConfigFileView, type AgentConfigSaveFile } from '@/api/endpoints/agent-config';
import { AgentConfigHeader } from './AgentConfigHeader';
import { AgentConfigPanel, AgentConfigPanels } from './AgentConfigPanels';
import { AgentConfigTextEditor } from './AgentConfigTextEditor';
import { AgentModelGroupSelect, type AgentModelGroup } from './AgentModelGroupSelect';

const NONE = '__keep__';
type View = 'quick' | 'file';
type Draft = { content: string; baseModifiedAt: number };

// 只读取界面摘要所需的字符串；TOML 格式校验和配置写入由后端完成。
function tomlString(content: string, key: string, table = ''): string {
    let currentTable = '';
    for (const line of content.split(/\r?\n/)) {
        const header = line.trim().match(/^\[([^\]]+)\]\s*(?:#.*)?$/);
        if (header) {
            currentTable = header[1].trim().replace(/\s*\.\s*/g, '.').replace(/["']/g, '');
            continue;
        }
        if (currentTable !== table) continue;
        const assignment = line.match(/^\s*([\w-]+)\s*=\s*("(?:\\.|[^"\\])*"|'[^']*')\s*(?:#.*)?$/);
        if (assignment?.[1] !== key) continue;
        if (assignment[2].startsWith("'")) return assignment[2].slice(1, -1);
        try { return JSON.parse(assignment[2]) as string; } catch { return ''; }
    }
    return '';
}

function authToken(content: string): string {
    try {
        const value: unknown = JSON.parse(content);
        if (typeof value === 'object' && value !== null && 'OPENAI_API_KEY' in value && typeof value.OPENAI_API_KEY === 'string') return value.OPENAI_API_KEY;
    } catch { /* 缺失或手工编辑中的 auth.json 不影响 TOML 编辑。 */ }
    return '';
}

function errorMessage(error: unknown, fallback: string): string {
    return error instanceof Error ? error.message : typeof error === 'object' && error !== null && 'message' in error && typeof error.message === 'string' ? error.message : fallback;
}

export function CodexConfigContent() {
    const t = useTranslations('topology');
    const instanceId = useId();
    const quickTabRef = useRef<HTMLButtonElement>(null);
    const fileTabRef = useRef<HTMLButtonElement>(null);
    const configQuery = useAgentConfig('codex');
    const keysQuery = useAPIKeyList();
    const groupsQuery = useGroupList();
    const createKey = useCreateAPIKey();
    const autoConfig = useAgentAutoConfig('codex');
    const saveConfig = useSaveAgentConfig('codex');
    const [view, setView] = useState<View>('quick');
    const [activeFileKey, setActiveFileKey] = useState('config');
    const [drafts, setDrafts] = useState<Record<string, Draft>>({});
    const [keyOverride, setKeyOverride] = useState<number>();
    const [createdKey, setCreatedKey] = useState<APIKey>();
    const [modelGroupId, setModelGroupId] = useState(0);
    const [processing, setProcessing] = useState(false);
    const [conflict, setConflict] = useState(false);

    const files = configQuery.data?.files ?? [];
    const configFile = files.find((file) => file.key === 'config');
    const authFile = files.find((file) => file.key === 'auth');
    const activeFile = files.find((file) => file.key === activeFileKey) ?? configFile;
    const contentFor = (file: AgentConfigFileView) => drafts[file.key]?.content ?? file.content;
    const configContent = configFile ? contentFor(configFile) : '';
    const provider = tomlString(configContent, 'model_provider') || 'openai';
    const token = tomlString(configContent, 'experimental_bearer_token', `model_providers.${provider}`)
        || tomlString(configContent, 'experimental_bearer_token')
        || (authFile ? authToken(contentFor(authFile)) : '');
    const currentModel = tomlString(configContent, 'model');
    const keys = [...(keysQuery.data ?? [])];
    if (createdKey && !keys.some((key) => key.id === createdKey.id)) keys.push(createdKey);
    const currentKey = keys.find((key) => key.api_key === token);
    const keyId = keyOverride ?? currentKey?.id ?? 0;
    const selectedKey = keys.find((key) => key.id === keyId);
    const validKey = !!selectedKey?.enabled;
    const allowed = selectedKey?.supported_models?.trim() ? new Set(selectedKey.supported_models.split(',').map((name) => name.trim())) : undefined;
    const groups = (groupsQuery.data ?? []).filter((group): group is AgentModelGroup => typeof group.id === 'number' && group.id > 0).sort((a, b) => a.name.localeCompare(b.name));
    const dirtyFiles = files.filter((file) => drafts[file.key] !== undefined && contentFor(file) !== file.content);
    const quickPending = (keyOverride !== undefined && keyOverride !== currentKey?.id) || modelGroupId > 0;
    const busy = processing || autoConfig.isPending || saveConfig.isPending || createKey.isPending;
    const hasChanges = quickPending || dirtyFiles.length > 0;
    const canSave = !!configFile && !busy && !conflict && (view === 'file' ? dirtyFiles.length > 0 : hasChanges && (!quickPending || validKey));

    const resetQuick = () => { setKeyOverride(undefined); setModelGroupId(0); };
    const putDraft = (file: AgentConfigFileView, content: string) => {
        setDrafts((previous) => ({ ...previous, [file.key]: { content, baseModifiedAt: previous[file.key]?.baseModifiedAt ?? file.modified_at } }));
    };

    const prepareDraft = async (): Promise<string> => {
        if (!configFile) throw new Error(t('configLoadFailed'));
        if (!validKey) throw new Error(t('codexConfig.selectKeyError'));
        const result = await autoConfig.mutateAsync({
            api_key_id: keyId,
            codex_model_group_id: modelGroupId || undefined,
            current_content: configContent,
        });
        putDraft(configFile, result.content);
        resetQuick();
        return result.content;
    };

    const changeView = async (next: View): Promise<boolean> => {
        if (next === view || busy) return false;
        setProcessing(true);
        try {
            if (next === 'file' && quickPending && validKey && !conflict) await prepareDraft();
            setView(next);
            return true;
        } catch (error) {
            toast.error(errorMessage(error, t('autoConfigFailed')));
            // 格式错误时仍可进入文件页修正，不让快捷配置阻断原始文件编辑。
            setView('file');
            return true;
        } finally { setProcessing(false); }
    };

    const handleSave = async () => {
        if (!configFile || !canSave) return;
        setProcessing(true);
        try {
            let inputs: AgentConfigSaveFile[] = dirtyFiles.map((file) => ({ key: file.key, content: contentFor(file), base_modified_at: drafts[file.key].baseModifiedAt }));
            if (view === 'quick' && quickPending) {
                const baseModifiedAt = drafts.config?.baseModifiedAt ?? configFile.modified_at;
                const content = await prepareDraft();
                inputs = [{ key: 'config', content, base_modified_at: baseModifiedAt }, ...inputs.filter((input) => input.key !== 'config')];
            }
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
                toast.error(t('codexConfig.conflict'));
            } else { toast.error(errorMessage(error, t('configSaveFailed'))); }
        } finally { setProcessing(false); }
    };

    const reload = async () => {
        setProcessing(true);
        try {
            const refreshed = await configQuery.refetch();
            if (refreshed.error) throw refreshed.error;
            setDrafts({}); setConflict(false); resetQuick();
        } catch (error) { toast.error(errorMessage(error, t('configLoadFailed'))); }
        finally { setProcessing(false); }
    };

    const handleCreateKey = () => {
        createKey.mutate({ name: 'codex-auto', enabled: true }, {
            onSuccess: (key) => { setCreatedKey(key); setKeyOverride(key.id); toast.success(t('autoConfigKeyCreated')); },
            onError: (error) => toast.error(errorMessage(error, t('autoConfigFailed'))),
        });
    };

    return (
        <>
            <AgentConfigHeader agent="codex" description={t('codexConfig.description')} />
            <Tabs value={view} onValueChange={(next) => { if (next === 'quick' || next === 'file') void changeView(next); }} className="min-h-0 flex-1 gap-0">
                <TabsList aria-label={t('codexConfig.views')} className="shrink-0 rounded-xl" onKeyDown={(event) => {
                    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
                    event.preventDefault();
                    const next = event.key === 'Home' ? 'quick' : event.key === 'End' ? 'file' : view === 'quick' ? 'file' : 'quick';
                    void changeView(next).then((changed) => { if (changed) (next === 'quick' ? quickTabRef : fileTabRef).current?.focus(); });
                }}>
                    <TabsTrigger ref={quickTabRef} value="quick" id={`${instanceId}-quick-tab`} aria-controls={`${instanceId}-quick-panel`} aria-selected={view === 'quick'} tabIndex={view === 'quick' ? 0 : -1} disabled={busy} className="gap-1.5 rounded-lg px-3"><SlidersHorizontal className="size-3.5" />{t('codexConfig.quickTab')}</TabsTrigger>
                    <TabsTrigger ref={fileTabRef} value="file" id={`${instanceId}-file-tab`} aria-controls={`${instanceId}-file-panel`} aria-selected={view === 'file'} tabIndex={view === 'file' ? 0 : -1} disabled={busy} className="gap-1.5 rounded-lg px-3"><FileCode2 className="size-3.5" />{t('codexConfig.fileTab')}</TabsTrigger>
                </TabsList>
                <motion.div layout="position" className="flex min-h-0 flex-1 flex-col pt-5">
                    {configQuery.isLoading && <div className="flex items-center justify-center gap-2 py-16 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />{t('configLoading')}</div>}
                    {configQuery.error && <p role="alert" className="mb-4 rounded-xl bg-destructive/10 p-3 text-sm text-destructive">{t('configLoadFailed')}</p>}
                    {conflict && <div role="alert" className="mb-4 flex flex-wrap items-center gap-3 rounded-xl bg-destructive/10 p-3 text-sm text-destructive"><span className="min-w-0 flex-1">{t('codexConfig.conflict')}</span><Button variant="outline" size="sm" disabled={busy} onClick={() => void reload()}>{t('configReload')}</Button></div>}
                    {configFile && <AgentConfigPanels>
                        <AgentConfigPanel value="quick" id={`${instanceId}-quick-panel`} aria-labelledby={`${instanceId}-quick-tab`} className="space-y-6">
                            <section className="space-y-3">
                                <div className="space-y-1"><h3 className="flex items-center gap-2 text-sm font-medium"><KeyRound className="size-4 text-muted-foreground" />{t('codexConfig.connection')}</h3><p className="text-xs text-muted-foreground">{t('codexConfig.connectionHint')}</p></div>
                                <div className="flex flex-wrap items-end gap-2"><div className="min-w-40 flex-1 space-y-1.5"><label htmlFor={`${instanceId}-key`} className="text-xs text-muted-foreground">{t('codexConfig.apiKey')}</label>
                                    <Select value={keyId ? String(keyId) : NONE} onValueChange={(next) => setKeyOverride(next === NONE ? 0 : Number(next))} disabled={busy || keysQuery.isLoading}>
                                        <SelectTrigger id={`${instanceId}-key`} className="w-full rounded-xl bg-background shadow-none"><SelectValue placeholder={t('autoConfigSelectKey')} /></SelectTrigger>
                                        <SelectContent className="z-[70] rounded-xl"><SelectItem value={NONE}>{t('autoConfigSelectKey')}</SelectItem>{keys.map((key) => <SelectItem key={key.id} value={String(key.id)} disabled={!key.enabled} className="rounded-lg">{key.name}{!key.enabled ? ` ${t('codexConfig.disabledKey')}` : ''}</SelectItem>)}</SelectContent>
                                    </Select>
                                </div><Button variant="outline" className="h-9 rounded-xl" disabled={busy} onClick={handleCreateKey}>{createKey.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Plus className="size-4" />}{t('autoConfigCreateKey')}</Button></div>
                                {keysQuery.error && <p role="alert" className="text-xs text-destructive">{t('codexConfig.keysFailed')}</p>}
                            </section>
                            <section className="space-y-3 border-t border-border/60 pt-5">
                                <div className="space-y-1"><h3 className="text-sm font-medium">{t('codexConfig.models')}</h3><p className="text-xs text-muted-foreground">{t('codexConfig.modelsHint')}</p></div>
                                <div className="space-y-1.5"><label htmlFor={`${instanceId}-model`} className="text-xs text-muted-foreground">{t('codexConfig.defaultModel')}</label><AgentModelGroupSelect id={`${instanceId}-model`} value={modelGroupId} groups={groups} allowed={allowed} placeholder={t('codexConfig.keepModels')} disabled={busy || groupsQuery.isLoading} onChange={setModelGroupId} /></div>
                                {currentModel && <p className="truncate text-xs text-muted-foreground" title={currentModel}>{t('codexConfig.currentModel', { model: currentModel })}</p>}
                                {groupsQuery.error && <p role="alert" className="text-xs text-destructive">{t('codexConfig.groupsFailed')}</p>}
                                {!groupsQuery.isLoading && !groupsQuery.error && groups.length === 0 && <p className="text-xs text-muted-foreground">{t('codexConfig.noGroups')}</p>}
                                {allowed && <p className="text-xs text-muted-foreground">{t('codexConfig.restrictedModels')}</p>}
                                <p className="text-xs text-muted-foreground">{t('codexConfig.moreSettings')}</p>
                            </section>
                        </AgentConfigPanel>
                        {activeFile && <AgentConfigPanel value="file" scrollable={false} id={`${instanceId}-file-panel`} aria-labelledby={`${instanceId}-file-tab`} className="flex flex-col gap-3 pb-1">
                            <div className="flex shrink-0 flex-wrap items-center justify-between gap-3"><Select value={activeFile.key} onValueChange={setActiveFileKey} disabled={busy}><SelectTrigger aria-label={t('codexConfig.selectFile')} className="rounded-xl bg-background shadow-none"><SelectValue /></SelectTrigger><SelectContent className="z-[70] rounded-xl">{files.map((file) => <SelectItem key={file.key} value={file.key} className="rounded-lg">{file.key === 'config' ? 'config.toml' : file.key === 'auth' ? 'auth.json' : file.key}{drafts[file.key] && contentFor(file) !== file.content ? ` · ${t('configUnsaved')}` : ''}</SelectItem>)}</SelectContent></Select><span className="rounded-md bg-muted px-2 py-0.5 text-xs uppercase text-muted-foreground">{activeFile.language}</span></div>
                            <p className="shrink-0 truncate font-mono text-xs text-muted-foreground" title={activeFile.path}>{activeFile.path}</p>
                            <AgentConfigTextEditor
                                fileKey={activeFile.key}
                                active={view === 'file'}
                                label={t('codexConfig.sourceLabel', { file: activeFile.key === 'auth' ? 'auth.json' : 'config.toml' })}
                                value={contentFor(activeFile)}
                                onChange={(content) => { putDraft(activeFile, content); resetQuick(); }}
                                disabled={busy}
                                placeholder={t('configNewFilePlaceholder')}
                            />
                        </AgentConfigPanel>}
                    </AgentConfigPanels>}
                </motion.div>
            </Tabs>
            <footer className="mt-5 flex shrink-0 flex-wrap items-center justify-between gap-3 border-t border-border/60 pt-4"><div className="min-w-0 space-y-1"><p className="flex items-center gap-1.5 text-xs text-muted-foreground">{hasChanges ? <span className="size-1.5 rounded-full bg-amber-500" /> : <Check className="size-3" />}{hasChanges ? t('configUnsaved') : t('configInSync')}<span className="ml-1 font-mono">{dirtyFiles.length > 1 ? t('codexConfig.fileCount', { count: dirtyFiles.length }) : activeFile?.key === 'auth' && view === 'file' ? 'auth.json' : 'config.toml'}</span></p><p className="text-xs text-muted-foreground">{t('codexConfig.saveHint')}</p></div><Button className="min-w-28 rounded-xl" disabled={!canSave} onClick={() => void handleSave()}>{processing || saveConfig.isPending ? <LoaderCircle className="size-4 animate-spin" /> : null}{t('codexConfig.save')}</Button></footer>
        </>
    );
}
