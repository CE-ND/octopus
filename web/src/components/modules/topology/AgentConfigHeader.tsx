'use client';

import { useTranslations } from 'next-intl';
import { FileCog, X } from 'lucide-react';
import {
    MorphingDialogClose,
    MorphingDialogDescription,
    MorphingDialogTitle,
} from '@/components/ui/morphing-dialog';
import { agentDisplayName } from './AgentIcon';

export function AgentConfigHeader({ agent, description }: { agent: string; description?: string }) {
    const t = useTranslations('topology');

    return (
        <>
            <MorphingDialogTitle className="shrink-0">
                <header className="flex items-start justify-between gap-3">
                    <h2 className="flex min-w-0 items-center gap-2 truncate text-lg font-bold text-card-foreground">
                        <FileCog className="size-5 shrink-0" />
                        {t('editConfigTitle', { agent: agentDisplayName(agent) })}
                    </h2>
                    {/* 按钮参与标题栏布局，形变结束时定位参照不会切换。 */}
                    <MorphingDialogClose className="relative right-0 top-0 shrink-0 rounded-lg p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground">
                        <X className="size-4" />
                    </MorphingDialogClose>
                </header>
            </MorphingDialogTitle>
            <MorphingDialogDescription className="shrink-0 pb-3 text-xs text-muted-foreground">
                {description ?? t('editConfigDescription')}
            </MorphingDialogDescription>
        </>
    );
}
