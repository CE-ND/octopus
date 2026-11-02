import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../client';

/**
 * 获取后端当前版本 Hook
 *
 * 后端: GET /api/v1/update/now-version -> string
 */
export function useNowVersion() {
    return useQuery({
        queryKey: ['update', 'now-version'],
        queryFn: async () => {
            return apiClient.get<string>('/api/v1/update/now-version');
        },
        refetchInterval: 3600000, // 1 小时
    });
}

export interface VersionInfo {
    version: string;
    commit: string;
    build_time: string;
    repository: string;
    releases_url: string;
    platform: string;
    architecture: string;
    desktop: boolean;
}

export interface ReleaseAsset {
    name: string;
    size: number;
    download_url: string;
    platform: string;
}

export interface ReleaseVersion {
    id: number;
    version: string;
    name: string;
    url: string;
    notes: string;
    published_at: string;
    prerelease: boolean;
    assets: ReleaseAsset[];
}

export type ReleaseStatus = 'update_available' | 'up_to_date' | 'ahead' | 'development' | 'unknown_version' | 'no_release';

export interface ReleaseCheck {
    current_version: string;
    status: ReleaseStatus;
    checked_at: string;
    latest: ReleaseVersion | null;
    releases: ReleaseVersion[];
}

export function useVersionInfo() {
    return useQuery({
        queryKey: ['update', 'info'],
        queryFn: () => apiClient.get<VersionInfo>('/api/v1/update/info'),
        staleTime: 3600000,
    });
}

const releaseQueryKey = ['update', 'releases'] as const;

export function useReleaseInfo() {
    return useQuery({
        queryKey: releaseQueryKey,
        queryFn: () => apiClient.get<ReleaseCheck>('/api/v1/update/releases'),
        staleTime: 10 * 60 * 1000,
        retry: false,
    });
}

// 手动检查刷新服务端发布缓存，成功后同步更新历史列表和更新提示。
export function useCheckForUpdates() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: () => apiClient.get<ReleaseCheck>('/api/v1/update/releases', { refresh: true }),
        onSuccess: (data) => queryClient.setQueryData(releaseQueryKey, data),
    });
}
