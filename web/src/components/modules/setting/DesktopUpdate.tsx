'use client';

import { useFormatter, useTranslations } from 'next-intl';
import { Download, Loader2, RefreshCw, RotateCw, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import type { DesktopUpdateAction, DesktopUpdateState } from '@/hooks/use-desktop-update';

export function DesktopUpdate({ state, bridgeError, onAction }: {
    state: DesktopUpdateState | null;
    bridgeError: boolean;
    onAction: (action: DesktopUpdateAction) => Promise<void>;
}) {
    const t = useTranslations('setting.info.desktopUpdate');
    const format = useFormatter();
    if (bridgeError) return <p role="alert" className="text-xs text-destructive">{t('bridgeError')}</p>;
    if (!state) return null;
    if (!state.supported) return <p className="text-xs text-muted-foreground">{t(`unavailable.${state.unavailable || 'platform'}`)}</p>;

    const downloading = state.phase === 'downloading' || state.phase === 'cancelling';
    const busy = downloading || state.phase === 'checking' || state.phase === 'installing';
    const retry = state.phase === 'error' ? state.errorOperation || 'check' : null;
    const action = state.phase === 'downloaded' ? 'install' : state.phase === 'available' ? 'download' : retry;
    const percent = state.progress?.percent ?? 0;
    const progressLabel = state.phase === 'downloading' && percent >= 100 ? t('verifying') : t(`phase.${state.phase}`, { version: state.version || '' });
    const size = (bytes: number) => format.number(bytes / 1000000, { maximumFractionDigits: 1 });

    return (
        <div className="space-y-3 rounded-xl bg-primary/5 p-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="space-y-1">
                    <p aria-live="polite" className="flex items-center gap-2 text-sm font-medium">
                        {busy && <Loader2 className="size-4 animate-spin" />}
                        {progressLabel}
                    </p>
                    <p className="text-xs text-muted-foreground">{t('installHint')}</p>
                </div>
                {action && <Button size="sm" className="rounded-xl" onClick={() => void onAction(action)}>
                    {action === 'install' ? <RotateCw className="size-4" /> : action === 'download' ? <Download className="size-4" /> : <RefreshCw className="size-4" />}
                    {retry ? t('retry') : action === 'install' ? t('install') : t('download')}
                </Button>}
            </div>
            {downloading && <div className="space-y-2">
                <div role="progressbar" aria-label={t('downloadProgress')} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.floor(percent)} className="h-1.5 overflow-hidden rounded-full bg-primary/10">
                    <div className="h-full rounded-full bg-primary transition-[width]" style={{ width: `${percent}%` }} />
                </div>
                <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
                    <span>{state.progress ? `${Math.floor(percent)}% · ${size(state.progress.transferred)} / ${size(state.progress.total)} MB · ${size(state.progress.bytesPerSecond)} MB/s` : t('connecting')}</span>
                    <Button variant="ghost" size="sm" className="h-7 rounded-lg text-xs" disabled={state.phase === 'cancelling'} onClick={() => void onAction('cancel')}><X className="size-3" />{t('cancel')}</Button>
                </div>
            </div>}
            {state.error && <p role="alert" className="text-xs text-destructive">{t(`errors.${state.error}`)}</p>}
        </div>
    );
}
