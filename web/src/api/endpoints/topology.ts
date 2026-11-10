'use client';

import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { apiClient, API_BASE_URL } from '@/api/client';
import { logger } from '@/lib/logger';

/** 网关 → 分组 的一条调用链路 */
export interface TopologyGroupLink {
    group_id: number;
    group_name: string;
    model: string;
    protocol: string;
    active: number;
    calls: number;
    last_seen: number;
    last_success?: boolean | null;
}

/** 一个调用方（agent + 密钥）的聚合视图 */
export interface TopologyCaller {
    agent: string;
    api_key_name: string;
    active: number;
    calls: number;
    last_seen: number;
    groups: TopologyGroupLink[];
}

/** 本机安装发现结果 */
export interface TopologyInstalledAgent {
    agent: string;
    name: string;
    installed: boolean;
    has_config: boolean;
    auto_config: boolean;
}

export interface TopologyLiveView {
    now: number;
    installed: TopologyInstalledAgent[];
    callers: TopologyCaller[];
}

/** 活跃判定：只有存在在途请求才播放流光；last_seen 仅用于排序和展示。 */
export function isTopologyNodeActive(active: number): boolean {
    return active > 0;
}

const topologyQueryKey = ['topology', 'live'] as const;

/**
 * 实时调用拓扑：优先通过 SSE 接收后端 begin/end 快照；
 * SSE 不可用时退化为 2s 轮询，避免页面状态卡死。
 */
export function useTopologyLive() {
    const queryClient = useQueryClient();
    const [streamConnected, setStreamConnected] = useState(false);

    const query = useQuery({
        queryKey: topologyQueryKey,
        queryFn: () => apiClient.get<TopologyLiveView>('/api/v1/topology/live'),
        refetchInterval: streamConnected ? false : 2000,
        refetchIntervalInBackground: true,
    });

    useEffect(() => {
        let disposed = false;
        let source: EventSource | null = null;
        let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
        let attempt = 0;

        const scheduleReconnect = () => {
            if (disposed || reconnectTimer) return;
            const delay = Math.min(30000, 1000 * 2 ** attempt);
            attempt = Math.min(attempt + 1, 5);
            reconnectTimer = setTimeout(() => {
                reconnectTimer = null;
                void connect();
            }, delay);
        };

        const connect = async () => {
            try {
                const { token } = await apiClient.get<{ token: string }>('/api/v1/topology/stream-token');
                if (disposed) return;

                source?.close();
                const eventSource = new EventSource(
                    `${API_BASE_URL}/api/v1/topology/stream?token=${encodeURIComponent(token)}`
                );
                source = eventSource;

                eventSource.onopen = () => {
                    if (disposed) return;
                    attempt = 0;
                    setStreamConnected(true);
                };

                eventSource.onmessage = event => {
                    if (disposed) return;
                    try {
                        const view = JSON.parse(event.data) as TopologyLiveView;
                        queryClient.setQueryData(topologyQueryKey, view);
                        setStreamConnected(true);
                    } catch (error) {
                        logger.error('解析拓扑 SSE 数据失败:', error);
                    }
                };

                eventSource.onerror = () => {
                    if (disposed) return;
                    setStreamConnected(false);
                    eventSource.close();
                    if (source === eventSource) source = null;
                    scheduleReconnect();
                };
            } catch (error) {
                if (disposed) return;
                setStreamConnected(false);
                logger.error('连接拓扑 SSE 失败:', error);
                scheduleReconnect();
            }
        };

        void connect();

        return () => {
            disposed = true;
            if (reconnectTimer) clearTimeout(reconnectTimer);
            source?.close();
            source = null;
        };
    }, [queryClient]);

    return { data: query.data, isLoading: query.isLoading, error: query.error };
}
