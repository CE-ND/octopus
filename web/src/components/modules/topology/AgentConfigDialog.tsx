'use client';

import { useMemo, useState } from 'react';
import { motion, MotionConfig, type Transition } from 'motion/react';
import { useTranslations } from 'next-intl';
import { LoaderCircle, Plus, Sparkles } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { toast } from '@/components/common/Toast';
import { cn } from '@/lib/utils';
import {
    useAgentAutoConfig,
    useAgentConfig,
    useSaveAgentConfig,
    type AgentConfigFileView,
} from '@/api/endpoints/agent-config';
import { useAPIKeyList, useCreateAPIKey } from '@/api/endpoints/apikey';
import { AgentConfigHeader } from './AgentConfigHeader';
import { ClaudeCodeConfigContent } from './ClaudeCodeConfigContent';
import { CodexConfigContent } from './CodexConfigContent';
import { DeepSeekHarnessConfigContent } from './DeepSeekHarnessConfigContent';
import { MorphingDialogContent, useMorphingDialog } from '@/components/ui/morphing-dialog';
import { AgentConfigResizeProvider, useAgentConfigResizeState } from './AgentConfigResize';

const DRAFT_PREFIX = 'agent-config-draft';

export function AgentConfigDialogPanel({ agent, transition }: { agent: string; transition: Transition }) {
    const { isOpen } = useMorphingDialog();
    const { size, controls } = useAgentConfigResizeState();
    const content = useMemo(() => <AgentConfigContent agent={agent} />, [agent]);
    const resizedOpen = !!size && isOpen;
    const layoutTransition = useMemo(() => resizedOpen ? { ...transition, layout: { duration: 0 } } : transition, [resizedOpen, transition]);

    return (
        <AgentConfigResizeProvider value={controls}>
            <MotionConfig transition={layoutTransition}>
                <MorphingDialogContent
                    className="relative flex max-h-[calc(100vh-2rem)] w-[min(48rem,calc(100vw-2rem))] flex-col overflow-hidden rounded-3xl bg-card px-6 py-5 text-card-foreground shadow-2xl"
                    style={size ? { width: size.width, height: size.height, maxWidth: 'min(1040px, calc(100vw - 2rem))' } : undefined}
                    transition={layoutTransition}
                >
                    {/* 调整尺寸只改变布局，正文仍参与父级形变的缩放补偿。 */}
                    <motion.div layout="position" className="flex min-h-0 flex-1 flex-col">
                        {content}
                    </motion.div>
                </MorphingDialogContent>
            </MotionConfig>
        </AgentConfigResizeProvider>
    );
}

/**
 * Agent 配置编辑器内容：配合 MorphingDialog 使用，从卡片上的编辑按钮
 * 变形展开。按后端注册表渲染可编辑配置文件（第一期 Codex：
 * config.toml + auth.json），逐文件保存；草稿以 overlay 形式叠加在
 * 服务器内容之上，避免用 effect 同步表单状态。
 * 支持「自动配置章鱼环境」的 agent 额外渲染密钥选择 + 一键生成区块。
 */
export function AgentConfigContent({ agent }: { agent: string }) {
    if (agent === 'claude-code') return <ClaudeCodeConfigContent />;
    if (agent === 'codex') return <CodexConfigContent />;
    if (agent === 'deepseek-harness') return <DeepSeekHarnessConfigContent />;
    return <AgentConfigFileContent agent={agent} />;
}

function AgentConfigFileContent({ agent }: { agent: string }) {
    const t = useTranslations('topology');
    const configQuery = useAgentConfig(agent);
    const saveMutation = useSaveAgentConfig(agent);
    const autoConfigMutation = useAgentAutoConfig(agent);
    const apiKeyList = useAPIKeyList();
    const createAPIKey = useCreateAPIKey();
    const [selectedKeyId, setSelectedKeyId] = useState<number | null>(null);
    const [drafts, setDrafts] = useState<Record<string, string>>({});
    const [savingKeys, setSavingKeys] = useState<string[]>([]);

    const draftKey = (agentId: string, fileKey: string) => `${DRAFT_PREFIX}:${agentId}:${fileKey}`;
    const draftFor = (file: AgentConfigFileView) => drafts[draftKey(agent, file.key)] ?? file.content;
    const isDirty = (file: AgentConfigFileView) => {
        const draft = drafts[draftKey(agent, file.key)];
        return draft !== undefined && draft !== file.content;
    };
    const clearDraft = (fileKey: string) => {
        setDrafts((prev) => {
            const next = { ...prev };
            delete next[draftKey(agent, fileKey)];
            return next;
        });
    };
    const setDraft = (fileKey: string, content: string) => {
        setDrafts((prev) => ({ ...prev, [draftKey(agent, fileKey)]: content }));
    };

    const autoConfigFileKey = configQuery.data?.auto_config_file_key ?? '';
    const autoConfigSupported = !!autoConfigFileKey;

    const handleCreateKey = () => {
        createAPIKey.mutate(
            { name: `${agent}-auto`, enabled: true },
            {
                onSuccess: (created) => {
                    setSelectedKeyId(created.id);
                    toast.success(t('autoConfigKeyCreated'));
                },
                onError: () => toast.error(t('autoConfigFailed')),
            },
        );
    };

    const handleAutoConfig = () => {
        if (!selectedKeyId || !autoConfigFileKey) return;
        const targetFile = configQuery.data?.files.find((file) => file.key === autoConfigFileKey);
        autoConfigMutation.mutate({
            api_key_id: selectedKeyId,
            current_content: targetFile ? draftFor(targetFile) : undefined,
        }, {
            onSuccess: (res) => {
                setDraft(res.file_key, res.content);
                toast.success(t('autoConfigApplied'));
            },
            onError: () => toast.error(t('autoConfigFailed')),
        });
    };

    const handleSave = (file: AgentConfigFileView) => {
        const content = draftFor(file);
        setSavingKeys((prev) => [...prev, file.key]);
        saveMutation.mutate(
            [{ key: file.key, content, base_modified_at: file.modified_at }],
            {
                onSuccess: () => {
                    toast.success(t('configSaved'));
                    clearDraft(file.key);
                    configQuery.refetch();
                },
                onError: (err) => {
                    const conflict = (err as { code?: number }).code === 409;
                    toast.error(conflict ? t('configConflict') : t('configSaveFailed'));
                    // 冲突时刷新基线：草稿仍叠加显示，用户确认后可再次保存
                    if (conflict) configQuery.refetch();
                },
                onSettled: () => {
                    setSavingKeys((prev) => prev.filter((key) => key !== file.key));
                },
            },
        );
    };

    return (
        <>
            <AgentConfigHeader agent={agent} />

            {/* 正文参与位移和父级缩放补偿，保持原字号且无需等待动画结束。 */}
            <motion.div layout="position" className="min-h-0 flex-1 space-y-5 overflow-y-auto px-6 pb-6">
                {configQuery.isLoading && (
                    <div className="flex items-center justify-center gap-2 py-16 text-sm text-muted-foreground">
                        <LoaderCircle className="size-4 animate-spin" />
                        {t('configLoading')}
                    </div>
                )}
                {configQuery.error && (
                    <div className="rounded-2xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
                        {t('configLoadFailed')}
                    </div>
                )}
                {autoConfigSupported && (
                    <div className="space-y-3 rounded-2xl border border-primary/25 bg-primary/5 p-4">
                        <div className="flex items-center gap-2">
                            <Sparkles className="size-4 text-primary" />
                            <span className="text-sm font-semibold text-card-foreground">
                                {t('autoConfigTitle')}
                            </span>
                        </div>
                        <p className="text-xs text-muted-foreground">{t('autoConfigDescription')}</p>
                        <div className="flex flex-wrap items-center gap-2">
                            <select
                                value={selectedKeyId ?? ''}
                                onChange={(e) => setSelectedKeyId(e.target.value ? Number(e.target.value) : null)}
                                className="h-8 min-w-40 rounded-lg border border-border bg-background px-2 text-xs text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                            >
                                <option value="">{t('autoConfigSelectKey')}</option>
                                {apiKeyList.data?.map((key) => (
                                    <option key={key.id} value={key.id}>
                                        {key.name}
                                    </option>
                                ))}
                            </select>
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                className="rounded-xl"
                                disabled={createAPIKey.isPending}
                                onClick={handleCreateKey}
                            >
                                {createAPIKey.isPending ? (
                                    <LoaderCircle className="size-4 animate-spin" />
                                ) : (
                                    <Plus className="size-4" />
                                )}
                                {t('autoConfigCreateKey')}
                            </Button>
                            <Button
                                type="button"
                                size="sm"
                                className="ml-auto rounded-xl"
                                disabled={!selectedKeyId || autoConfigMutation.isPending}
                                onClick={handleAutoConfig}
                            >
                                {autoConfigMutation.isPending ? (
                                    <LoaderCircle className="size-4 animate-spin" />
                                ) : (
                                    <Sparkles className="size-4" />
                                )}
                                {t('autoConfigApply')}
                            </Button>
                        </div>
                    </div>
                )}
                {configQuery.data?.files.map((file) => {
                    const value = draftFor(file);
                    const dirty = isDirty(file);
                    const saving = savingKeys.includes(file.key);
                    return (
                        <section key={file.key} className="space-y-2">
                            <div className="flex items-center gap-2">
                                <h3 className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground" title={file.path}>
                                    {file.path}
                                </h3>
                                <Badge variant="secondary" className="rounded-lg text-[10px] uppercase">
                                    {file.language}
                                </Badge>
                                {file.exists && !dirty && <span className="text-[10px] text-muted-foreground">{t('configInSync')}</span>}
                                {dirty && <span className="text-[10px] text-amber-600 dark:text-amber-400">{t('configUnsaved')}</span>}
                                <Button
                                    type="button"
                                    size="sm"
                                    className="rounded-xl"
                                    disabled={!dirty || saving}
                                    onClick={() => handleSave(file)}
                                >
                                    {saving ? <LoaderCircle className="size-4 animate-spin" /> : null}
                                    {t('configSave')}
                                </Button>
                            </div>
                            <textarea
                                value={value}
                                onChange={(e) => setDraft(file.key, e.target.value)}
                                spellCheck={false}
                                placeholder={t('configNewFilePlaceholder')}
                                className={cn(
                                    'h-64 w-full resize-y rounded-xl border border-border bg-background p-3 font-mono text-xs leading-relaxed text-foreground',
                                    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                                )}
                            />
                        </section>
                    );
                })}
            </motion.div>
        </>
    );
}
