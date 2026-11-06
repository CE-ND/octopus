'use client';

import { useFormatter, useTranslations } from 'next-intl';
import { Download, ExternalLink, History, Loader2, RefreshCw } from 'lucide-react';
import { useCheckForUpdates, useReleaseInfo, type ReleaseAsset, type ReleaseVersion } from '@/api/endpoints/update';
import { Button } from '@/components/ui/button';
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion';
import { useDesktopUpdate } from '@/hooks/use-desktop-update';
import { DesktopUpdate } from './DesktopUpdate';

function ReleaseDownloads({ assets }: { assets: ReleaseAsset[] }) {
    const t = useTranslations('setting.info');
    const format = useFormatter();

    function assetLabel(asset: ReleaseAsset) {
        const name = asset.name.toLowerCase();
        if (asset.platform === 'windows') return t('platformWindows');
        if (asset.platform === 'darwin') {
            if (name.includes('arm64') || name.includes('aarch64')) return t('platformMacArm');
            if (name.includes('x64') || name.includes('amd64') || name.includes('x86_64')) return t('platformMacIntel');
            return 'macOS';
        }
        if (asset.platform === 'linux') return 'Linux';
        return asset.name;
    }

    if (!assets.length) {
        return <p className="text-xs text-muted-foreground">{t('noAssets')}</p>;
    }

    return (
        <div className="flex flex-wrap gap-2">
            {assets.map((asset) => (
                <Button key={asset.download_url} asChild variant="outline" size="sm" className="h-auto max-w-full rounded-xl py-2">
                    <a href={asset.download_url} target="_blank" rel="noopener noreferrer" title={asset.name} aria-label={t('downloadPackage', { name: asset.name })}>
                        <Download className="size-3.5 shrink-0" />
                        <span className="min-w-0 whitespace-normal break-all text-left">
                            <span className="block">{assetLabel(asset)}</span>
                            {assetLabel(asset) !== asset.name && <span className="block text-[10px] font-normal text-muted-foreground">{asset.name}</span>}
                        </span>
                        {asset.size > 0 && <span className="shrink-0 text-xs text-muted-foreground">{format.number(asset.size / 1000000, { maximumFractionDigits: 1 })} MB</span>}
                    </a>
                </Button>
            ))}
        </div>
    );
}

function ReleaseDetails({ release }: { release: ReleaseVersion }) {
    const t = useTranslations('setting.info');
    return (
        <div className="space-y-3">
            {release.notes && <div className="max-h-60 overflow-y-auto whitespace-pre-wrap break-words text-xs leading-relaxed text-muted-foreground">{release.notes}</div>}
            <ReleaseDownloads assets={release.assets} />
            <a href={release.url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1.5 text-xs text-primary hover:underline">
                {t('viewRelease')}<ExternalLink className="size-3" />
            </a>
        </div>
    );
}

export function VersionReleases({ releasesURL }: { releasesURL: string }) {
    const t = useTranslations('setting.info');
    const format = useFormatter();
    const releases = useReleaseInfo();
    const check = useCheckForUpdates();
    const desktop = useDesktopUpdate();
    const inAppUpdate = desktop.state?.supported === true;
    const data = releases.data;
    const latest = data?.latest;
    const checking = releases.isFetching || check.isPending || desktop.state?.phase === 'checking';
    const updating = desktop.state?.phase === 'downloading' || desktop.state?.phase === 'cancelling' || desktop.state?.phase === 'installing';
    const error = check.error || releases.error;
    const rateLimited = !!error && 'errorCode' in error && error.errorCode === 'update.github_rate_limited';

    function formatDate(value: string) {
        const date = new Date(value);
        return Number.isNaN(date.getTime()) || date.getFullYear() < 2000
            ? t('unknown')
            : format.dateTime(date, { dateStyle: 'medium', timeStyle: 'short' });
    }

    return (
        <div className="space-y-4 border-t border-border pt-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
                <Button onClick={() => { check.mutate(); if (inAppUpdate) void desktop.run('check'); }} disabled={checking || updating} size="sm" className="rounded-xl">
                    {checking ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
                    {checking ? t('checking') : t('checkUpdates')}
                </Button>
                <a href={releasesURL} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1.5 text-xs text-primary hover:underline">
                    {t('allReleases')}<ExternalLink className="size-3" />
                </a>
            </div>

            {error && <p role="alert" className="text-xs text-destructive">{rateLimited ? t('rateLimited') : t('checkFailed')}</p>}

            <DesktopUpdate state={desktop.state} bridgeError={desktop.bridgeError} onAction={desktop.run} />

            {data && (
                <div aria-live="polite" className="space-y-3">
                    {!inAppUpdate && <p className={data.status === 'update_available' ? 'text-sm font-medium text-primary' : 'text-sm text-muted-foreground'}>
                        {t(`updateStatus.${data.status}`, { version: latest?.version || '' })}
                    </p>}
                    {latest && (
                        <div className="space-y-3 rounded-xl border border-border bg-muted/30 p-3">
                            <div className="flex flex-wrap items-center justify-between gap-2">
                                <span className="text-sm font-medium">{t('latestVersion')} <code>{latest.version}</code></span>
                                <span className="text-xs text-muted-foreground">{formatDate(latest.published_at)}</span>
                            </div>
                            {inAppUpdate ? <details className="text-xs text-muted-foreground">
                                <summary className="cursor-pointer py-1">{t('desktopUpdate.manualDownloads')}</summary>
                                <div className="pt-2"><ReleaseDownloads assets={latest.assets} /></div>
                            </details> : <ReleaseDownloads assets={latest.assets} />}
                            <a href={latest.url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1.5 text-xs text-primary hover:underline">
                                {t('viewRelease')}<ExternalLink className="size-3" />
                            </a>
                        </div>
                    )}
                    <p className="text-xs text-muted-foreground">{t('lastChecked', { time: formatDate(data.checked_at) })}</p>
                </div>
            )}

            {data && (latest?.notes || data.releases.length > 0) && (
                <Accordion type="single" collapsible>
                    {latest?.notes && (
                        <AccordionItem value="latest-notes">
                            <AccordionTrigger className="py-3 text-sm">{t('releaseNotes')}</AccordionTrigger>
                            <AccordionContent>
                                <div className="max-h-60 overflow-y-auto whitespace-pre-wrap break-words text-xs leading-relaxed text-muted-foreground">{latest.notes}</div>
                            </AccordionContent>
                        </AccordionItem>
                    )}
                    {data.releases.length > 0 && (
                        <AccordionItem value="history">
                            <AccordionTrigger className="py-3 text-sm">
                                <span className="flex items-center gap-2"><History className="size-4" />{t('releaseHistory', { count: data.releases.length })}</span>
                            </AccordionTrigger>
                            <AccordionContent>
                                <Accordion type="single" collapsible className="max-h-[32rem] overflow-y-auto">
                                    {data.releases.map((release) => (
                                        <AccordionItem key={release.id} value={String(release.id)}>
                                            <AccordionTrigger className="py-3 text-sm">
                                                <span className="flex min-w-0 flex-col items-start gap-1">
                                                    <span className="flex flex-wrap items-center gap-2">
                                                        <code>{release.version}</code>
                                                        <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">{release.prerelease ? t('prerelease') : t('stableRelease')}</span>
                                                    </span>
                                                    <span className="text-xs font-normal text-muted-foreground">{formatDate(release.published_at)}</span>
                                                </span>
                                            </AccordionTrigger>
                                            <AccordionContent><ReleaseDetails release={release} /></AccordionContent>
                                        </AccordionItem>
                                    ))}
                                </Accordion>
                            </AccordionContent>
                        </AccordionItem>
                    )}
                </Accordion>
            )}
        </div>
    );
}
