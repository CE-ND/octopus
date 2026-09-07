'use client';

import { useMemo, useState } from 'react';
import { AppWindow, FolderOpen, MessagesSquare, RefreshCw, SquareTerminal, Terminal } from 'lucide-react';
import { useTranslations } from 'next-intl';
import type { Group } from '@/api/endpoints/group';
import { useCodexSessionRoutes, useGroupList, useUpdateCodexSessionRoute } from '@/api/endpoints/group';
import { Button } from '@/components/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { cn } from '@/lib/utils';

const AUTO_ROUTE_VALUE = 'auto';

function shortSessionID(sessionID: string) {
    return sessionID.slice(0, 8);
}

export function CodexSessionRoutingDialog({
    open,
    onOpenChange,
    scopeGroup,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    scopeGroup?: Group;
}) {
    const t = useTranslations('group.codexRouting');
    const [scopedOnly, setScopedOnly] = useState(false);
    const [sourceFilter, setSourceFilter] = useState<'all' | 'codex' | 'claude'>('all');
    const routes = useCodexSessionRoutes(open);
    const { data: groups } = useGroupList();
    const updateRoute = useUpdateCodexSessionRoute();
    const selectableGroups = useMemo(
        () => (groups ?? []).filter((group): group is Group & { id: number } => typeof group.id === 'number'),
        [groups],
    );

    const visibleSessions = useMemo(() => {
        let sessions = routes.data ?? [];
        if (sourceFilter === 'codex') sessions = sessions.filter((session) => session.source !== 'claude');
        if (sourceFilter === 'claude') sessions = sessions.filter((session) => session.source === 'claude');
        if (!scopeGroup) return sessions;
        if (scopedOnly) return sessions.filter((session) => session.group_id === scopeGroup.id);
        // “全部会话”视图下把本组会话排到最前，方便围绕当前分组操作（sort 是稳定的）
        return [...sessions].sort((a, b) =>
            (b.group_id === scopeGroup.id ? 1 : 0) - (a.group_id === scopeGroup.id ? 1 : 0),
        );
    }, [routes.data, scopeGroup, scopedOnly, sourceFilter]);

    const handleOpenChange = (next: boolean) => {
        if (!next) {
            setScopedOnly(false);
            setSourceFilter('all');
        }
        onOpenChange(next);
    };

    return (
        <Dialog open={open} onOpenChange={handleOpenChange}>
            <DialogContent className="h-[80vh] grid-rows-[auto_auto_minmax(0,1fr)] overflow-hidden rounded-2xl p-0 sm:max-w-3xl">
                <DialogHeader className="border-b px-6 py-5">
                    <DialogTitle className="flex items-center gap-2">
                        <MessagesSquare className="size-5 text-primary" />
                        {scopeGroup ? t('scopedTitle', { group: scopeGroup.name }) : t('dialogTitle')}
                    </DialogTitle>
                    <DialogDescription>{t('description')}</DialogDescription>
                </DialogHeader>

                <div className={cn('flex flex-wrap items-center justify-between gap-2 px-6 pt-3', !scopeGroup && 'justify-end')}>
                    {scopeGroup && (
                        <div className="inline-flex rounded-xl border border-border/60 bg-muted/30 p-0.5 text-xs">
                            <button
                                type="button"
                                onClick={() => setScopedOnly(false)}
                                className={cn(
                                    'rounded-[10px] px-3 py-1 transition-colors',
                                    !scopedOnly ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground',
                                )}
                            >
                                {t('filterAll')}
                            </button>
                            <button
                                type="button"
                                onClick={() => setScopedOnly(true)}
                                className={cn(
                                    'rounded-[10px] px-3 py-1 transition-colors',
                                    scopedOnly ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground',
                                )}
                            >
                                {t('filterGroup')}
                            </button>
                        </div>
                    )}
                    <div className="inline-flex rounded-xl border border-border/60 bg-muted/30 p-0.5 text-xs">
                        <button
                            type="button"
                            onClick={() => setSourceFilter('all')}
                            className={cn(
                                'rounded-[10px] px-3 py-1 transition-colors',
                                sourceFilter === 'all' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground',
                            )}
                        >
                            {t('filterSourceAll')}
                        </button>
                        <button
                            type="button"
                            onClick={() => setSourceFilter('codex')}
                            className={cn(
                                'inline-flex items-center gap-1 rounded-[10px] px-3 py-1 transition-colors',
                                sourceFilter === 'codex' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground',
                            )}
                        >
                            <Terminal className="size-3" />
                            {t('filterSourceCodex')}
                        </button>
                        <button
                            type="button"
                            onClick={() => setSourceFilter('claude')}
                            className={cn(
                                'inline-flex items-center gap-1 rounded-[10px] px-3 py-1 transition-colors',
                                sourceFilter === 'claude' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground',
                            )}
                        >
                            <SquareTerminal className="size-3" />
                            {t('filterSourceClaude')}
                        </button>
                    </div>
                </div>

                <div className="min-h-0 overflow-y-auto px-3 pb-3">
                    {routes.isLoading && (
                        <div className="flex min-h-full items-center justify-center gap-2 text-sm text-muted-foreground">
                            <RefreshCw className="size-4 animate-spin" />
                            {t('loading')}
                        </div>
                    )}

                    {routes.isError && (
                        <div className="m-3 rounded-xl border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">
                            {t('unavailable')}
                        </div>
                    )}

                    {!routes.isLoading && !routes.isError && visibleSessions.length === 0 && (
                        <div className="flex min-h-full flex-col items-center justify-center gap-2 text-center text-muted-foreground">
                            <MessagesSquare className="size-8 opacity-50" />
                            <p className="text-sm">{t('empty')}</p>
                        </div>
                    )}

                    <div className="divide-y divide-border/60">
                        {visibleSessions.map((session) => {
                            const currentValue = session.group_id > 0 ? String(session.group_id) : AUTO_ROUTE_VALUE;
                            const inScope = scopeGroup !== undefined && session.group_id === scopeGroup.id;
                            return (
                                <div
                                    key={session.session_id}
                                    className={cn(
                                        'flex flex-col gap-3 rounded-xl px-3 py-4 sm:flex-row sm:items-center',
                                        inScope && 'bg-primary/5',
                                    )}
                                >
                                    <div className="min-w-0 flex-1">
                                        <div className="flex min-w-0 items-center gap-2">
                                            {session.source === 'claude' ? (
                                                <span className="inline-flex shrink-0 items-center gap-1 rounded-md bg-secondary px-1.5 py-0.5 text-[10px] font-medium text-secondary-foreground">
                                                    <SquareTerminal className="size-3" />
                                                    {t('sourceClaude')}
                                                </span>
                                            ) : session.source === 'cli' ? (
                                                <span className="inline-flex shrink-0 items-center gap-1 rounded-md bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">
                                                    <Terminal className="size-3" />
                                                    {t('sourceCli')}
                                                </span>
                                            ) : (
                                                <span className="inline-flex shrink-0 items-center gap-1 rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                                                    <AppWindow className="size-3" />
                                                    {t('sourceDesktop')}
                                                </span>
                                            )}
                                            <div className="truncate text-sm font-medium text-foreground">
                                                {session.title || t('untitled')}
                                            </div>
                                        </div>
                                        <div className="mt-1 flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
                                            <FolderOpen className="size-3.5 shrink-0" />
                                            <span className="truncate">{session.cwd || t('unknownWorkspace')}</span>
                                            <span className="shrink-0 font-mono text-[10px] text-muted-foreground/70">
                                                {shortSessionID(session.session_id)}
                                            </span>
                                        </div>
                                        <div className="mt-2 inline-flex max-w-full items-center gap-1.5 rounded-md bg-muted px-2 py-1 text-[11px] text-muted-foreground">
                                            <span>{t('currentModel')}</span>
                                            <span className="truncate font-mono font-medium text-foreground">
                                                {session.current_model || t('modelUnknown')}
                                            </span>
                                        </div>
                                    </div>

                                    {scopeGroup ? (
                                        // 分组视角：只做「绑定到本组 / 解除绑定」，不出现其他分组选项
                                        <div className="flex w-full shrink-0 flex-col items-stretch gap-1 sm:w-auto sm:items-end">
                                            {session.group_id === scopeGroup.id ? (
                                                <Button
                                                    type="button"
                                                    variant="outline"
                                                    size="sm"
                                                    className="rounded-xl"
                                                    disabled={updateRoute.isPending || !session.current_model}
                                                    onClick={() => updateRoute.mutate({
                                                        sessionID: session.session_id,
                                                        requestModel: session.current_model,
                                                        groupID: 0,
                                                    })}
                                                >
                                                    {t('unbind')}
                                                </Button>
                                            ) : (
                                                <Button
                                                    type="button"
                                                    size="sm"
                                                    className="rounded-xl"
                                                    disabled={updateRoute.isPending || !session.current_model}
                                                    onClick={() => updateRoute.mutate({
                                                        sessionID: session.session_id,
                                                        requestModel: session.current_model,
                                                        groupID: scopeGroup.id!,
                                                    })}
                                                >
                                                    {t('bind')}
                                                </Button>
                                            )}
                                            {session.group_id > 0 && session.group_id !== scopeGroup.id && (
                                                <span className="text-[11px] text-muted-foreground">
                                                    {t('boundOther', { group: session.group_name || session.group_id })}
                                                </span>
                                            )}
                                        </div>
                                    ) : (
                                        <Select
                                            value={currentValue}
                                            disabled={updateRoute.isPending || !session.current_model}
                                            onValueChange={(value) => updateRoute.mutate({
                                                sessionID: session.session_id,
                                                requestModel: session.current_model,
                                                groupID: value === AUTO_ROUTE_VALUE ? 0 : Number(value),
                                            })}
                                        >
                                            <SelectTrigger className={cn(
                                                'w-full rounded-xl sm:w-56',
                                                session.group_id > 0 && 'border-primary/30 bg-primary/5',
                                            )}>
                                                <SelectValue placeholder={t('selectGroup')} />
                                            </SelectTrigger>
                                            <SelectContent align="end">
                                                <SelectItem value={AUTO_ROUTE_VALUE}>{t('automatic')}</SelectItem>
                                                {selectableGroups.map((group) => (
                                                    <SelectItem key={group.id} value={String(group.id)}>{group.name}</SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                    )}
                                </div>
                            );
                        })}
                    </div>
                </div>
            </DialogContent>
        </Dialog>
    );
}
