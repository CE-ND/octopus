'use client';

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import { Info, Tag, Github, AlertTriangle, Loader2, Globe } from 'lucide-react';
import { APP_VERSION, GITHUB_REPO } from '@/lib/info';
import { useNowVersion } from '@/api/endpoints/update';
import { API_BASE_URL } from '@/api/client';
import { CopyIconButton } from '@/components/common/CopyButton';
import { Button } from '@/components/ui/button';
import { isOctopusCacheName, isFontCacheName, SW_MESSAGE_TYPE } from '@/lib/sw';

// 解析对客户端暴露的完整请求地址（OpenAI 兼容端点前缀）：
// 显式配置了后端地址（开发模式）时用配置值，否则前端由后端托管、与页面同源
// 组件经 ContentLoader 客户端动态加载，初始化时 window 必然可用；SSG 预渲染兜底返回空串
function resolveRelayBaseUrl(): string {
    if (API_BASE_URL && API_BASE_URL !== '.') {
        return `${API_BASE_URL.replace(/\/+$/, '')}/v1`;
    }
    if (typeof window === 'undefined') return '';
    return `${window.location.origin}/v1`;
}

export function SettingInfo() {
    const t = useTranslations('setting');
    const nowVersionQuery = useNowVersion();
    const [relayBaseUrl] = useState(() => resolveRelayBaseUrl());

    const backendNowVersion = nowVersionQuery.data || '';

    // 前端版本与后端当前版本不一致 → 浏览器缓存问题
    const isCacheMismatch = !!backendNowVersion && backendNowVersion !== APP_VERSION;

    const clearCacheAndReload = async () => {
        // 通知 Service Worker 清理缓存
        if ('serviceWorker' in navigator && navigator.serviceWorker.controller) {
            navigator.serviceWorker.controller.postMessage({ type: SW_MESSAGE_TYPE.CLEAR_CACHE });
        }
        // 同时也从主线程清理（双保险），但保留字体缓存
        if ('caches' in window) {
            const names = await caches.keys();
            await Promise.all(
                names
                    .filter((name) => isOctopusCacheName(name) && !isFontCacheName(name))
                    .map((name) => caches.delete(name))
            );
        }
        // 注销当前 SW，下次加载会重新注册
        if ('serviceWorker' in navigator) {
            const registrations = await navigator.serviceWorker.getRegistrations();
            await Promise.all(registrations.map((reg) => reg.unregister()));
        }
        // 强制刷新（跳过缓存）
        window.location.reload();
    };

    const handleForceRefresh = () => {
        clearCacheAndReload();
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Info className="h-5 w-5" />
                {t('info.title')}
            </h2>
            {/* GitHub 仓库 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Github className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('info.github')}</span>
                </div>
                <a
                    href={GITHUB_REPO}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-sm text-primary hover:underline"
                >
                    {GITHUB_REPO.replace('https://github.com/', '')}
                </a>
            </div>
            {/* 请求地址 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Globe className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('info.requestUrl')}</span>
                </div>
                <div className="flex items-center gap-1">
                    <code className="text-sm font-mono text-muted-foreground">
                        {relayBaseUrl || t('info.unknown')}
                    </code>
                    {relayBaseUrl && (
                        <CopyIconButton
                            text={relayBaseUrl}
                            className="p-1 rounded-md transition-colors hover:bg-muted text-muted-foreground hover:text-foreground"
                            copyIconClassName="size-3.5"
                            checkIconClassName="size-3.5 text-primary"
                        />
                    )}
                </div>
            </div>
            {/* 当前版本 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Tag className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('info.currentVersion')}</span>
                </div>
                <div className="flex items-center gap-2">
                    {nowVersionQuery.isLoading ? (
                        <Loader2 className="size-4 animate-spin text-muted-foreground" />
                    ) : (
                        <code className="text-sm font-mono text-muted-foreground">
                            {backendNowVersion || t('info.unknown')}
                        </code>
                    )}
                </div>
            </div>

            {/* 浏览器缓存问题警告 */}
            {isCacheMismatch && (
                <div className="p-3 bg-destructive/10 border border-destructive/20 rounded-xl space-y-2">
                    <div className="flex items-start gap-3">
                        <AlertTriangle className="h-5 w-5 text-destructive shrink-0 mt-0.5" />
                        <div className="flex-1 space-y-1">
                            <p className="text-sm text-destructive font-medium">
                                {t('info.versionMismatch')}
                            </p>
                            <p className="text-xs text-muted-foreground">
                                {t('info.versionMismatchHint', { frontend: APP_VERSION, backend: backendNowVersion })}
                            </p>
                        </div>
                    </div>
                    <div className="flex justify-end">
                        <Button
                            variant="destructive"
                            size="sm"
                            onClick={handleForceRefresh}
                            className="rounded-xl"
                        >
                            {t('info.forceRefresh')}
                        </Button>
                    </div>
                </div>
            )}
        </div>
    );
}
