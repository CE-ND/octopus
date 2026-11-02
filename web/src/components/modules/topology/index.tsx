'use client';

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import { Waypoints } from 'lucide-react';
import { useTopologyLive } from '@/api/endpoints/topology';
import { resolveRelayBaseUrl } from '@/lib/relay-url';
import { TopologyCanvas } from './canvas';

export function Topology() {
    const t = useTranslations('topology');
    const { data, isLoading } = useTopologyLive();
    const [relayBaseUrl] = useState(() => resolveRelayBaseUrl());

    // 首次加载中先给骨架，避免闪空态
    if (isLoading) {
        return (
            <div className="flex h-full min-h-0 items-center justify-center rounded-t-3xl">
                <div className="flex flex-col items-center gap-3 text-muted-foreground">
                    <Waypoints className="size-8 animate-pulse" />
                    <span className="text-sm">{t('loading')}</span>
                </div>
            </div>
        );
    }

    const installedAny = (data?.installed ?? []).some((item) => item.installed);
    const hasCallers = (data?.callers.length ?? 0) > 0;

    // 既没有安装任何可识别的 agent，也从未有调用记录 → 引导空态
    if (!data || (!hasCallers && !installedAny)) {
        return (
            <div className="flex h-full min-h-0 items-center justify-center rounded-t-3xl p-6">
                <div className="flex max-w-md flex-col items-center gap-3 rounded-3xl border border-border bg-card p-8 text-center shadow-sm">
                    <Waypoints className="size-10 text-muted-foreground" />
                    <div className="text-base font-semibold text-card-foreground">
                        {t('empty.title')}
                    </div>
                    <p className="text-sm text-muted-foreground">
                        {t('empty.hint', { url: relayBaseUrl })}
                    </p>
                </div>
            </div>
        );
    }

    return (
        <div className="topology relative h-full min-h-0 overflow-hidden rounded-t-3xl">
            <TopologyCanvas view={data} />
            {/* 图例 */}
            <div className="pointer-events-none absolute bottom-3 left-1/2 flex -translate-x-1/2 items-center gap-4 rounded-full border border-border bg-card/90 px-4 py-1.5 text-xs text-muted-foreground shadow-sm backdrop-blur">
                <span className="flex items-center gap-1.5">
                    <svg width="22" height="6" aria-hidden>
                        <line
                            x1="0"
                            y1="3"
                            x2="22"
                            y2="3"
                            className="topology-legend-active"
                            strokeWidth="2"
                            strokeDasharray="6 4"
                        />
                    </svg>
                    {t('legend.active')}
                </span>
                <span className="flex items-center gap-1.5">
                    <svg width="22" height="6" aria-hidden>
                        <line
                            x1="0"
                            y1="3"
                            x2="22"
                            y2="3"
                            className="topology-legend-idle"
                            strokeWidth="2"
                            strokeDasharray="4 5"
                        />
                    </svg>
                    {t('legend.idle')}
                </span>
            </div>
        </div>
    );
}
