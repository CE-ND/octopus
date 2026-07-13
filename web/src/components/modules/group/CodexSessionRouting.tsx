'use client';

import { useMemo, useState } from 'react';
import { FolderOpen, MessagesSquare, RefreshCw, Route } from 'lucide-react';
import { useTranslations } from 'next-intl';
import type { Group } from '@/api/endpoints/group';
import { useCodexSessionRoutes, useUpdateCodexSessionRoute } from '@/api/endpoints/group';
import { Button } from '@/components/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
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

export function CodexSessionRouting({ groups }: { groups: Group[] }) {
    const t = useTranslations('group.codexRouting');
    const [open, setOpen] = useState(false);
    const routes = useCodexSessionRoutes(open);
    const updateRoute = useUpdateCodexSessionRoute();
    const selectableGroups = useMemo(
        () => groups.filter((group): group is Group & { id: number } => typeof group.id === 'number'),
        [groups],
    );

    return (
        <div className="mb-4 flex items-center justify-between rounded-2xl border border-border/60 bg-card/70 px-4 py-3">
            <div className="min-w-0">
                <div className="flex items-center gap-2 text-sm font-semibold">
                    <Route className="size-4 text-primary" />
                    {t('title')}
                </div>
                <p className="mt-0.5 truncate text-xs text-muted-foreground">{t('summary')}</p>
            </div>

            <Dialog open={open} onOpenChange={setOpen}>
                <DialogTrigger asChild>
                    <Button type="button" variant="outline" size="sm" className="ml-4 shrink-0 rounded-xl">
                        <MessagesSquare className="size-4" />
                        {t('manage')}
                    </Button>
                </DialogTrigger>
                <DialogContent className="max-h-[80vh] grid-rows-[auto_minmax(0,1fr)] overflow-hidden rounded-2xl p-0 sm:max-w-3xl">
                    <DialogHeader className="border-b px-6 py-5">
                        <DialogTitle className="flex items-center gap-2">
                            <MessagesSquare className="size-5 text-primary" />
                            {t('dialogTitle')}
                        </DialogTitle>
                        <DialogDescription>{t('description')}</DialogDescription>
                    </DialogHeader>

                    <div className="min-h-0 overflow-y-auto px-3 pb-3">
                        {routes.isLoading && (
                            <div className="flex min-h-48 items-center justify-center gap-2 text-sm text-muted-foreground">
                                <RefreshCw className="size-4 animate-spin" />
                                {t('loading')}
                            </div>
                        )}

                        {routes.isError && (
                            <div className="m-3 rounded-xl border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">
                                {t('unavailable')}
                            </div>
                        )}

                        {routes.data?.length === 0 && (
                            <div className="flex min-h-48 flex-col items-center justify-center gap-2 text-center text-muted-foreground">
                                <MessagesSquare className="size-8 opacity-50" />
                                <p className="text-sm">{t('empty')}</p>
                            </div>
                        )}

                        <div className="divide-y divide-border/60">
                            {routes.data?.map((session) => {
                                const currentValue = session.group_id > 0 ? String(session.group_id) : AUTO_ROUTE_VALUE;
                                return (
                                    <div key={session.session_id} className="flex flex-col gap-3 px-3 py-4 sm:flex-row sm:items-center">
                                        <div className="min-w-0 flex-1">
                                            <div className="truncate text-sm font-medium text-foreground">
                                                {session.title || t('untitled')}
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
                                    </div>
                                );
                            })}
                        </div>
                    </div>
                </DialogContent>
            </Dialog>
        </div>
    );
}
