'use client';

import { useCallback, useEffect, useState } from 'react';

export type DesktopUpdateAction = 'check' | 'download' | 'cancel' | 'install';
export interface DesktopUpdateState {
    revision: number;
    supported: boolean;
    unavailable: 'development' | 'platform' | 'not-installed' | 'missing-config' | null;
    currentVersion: string;
    phase: 'unsupported' | 'idle' | 'checking' | 'available' | 'up-to-date' | 'downloading' | 'cancelling' | 'downloaded' | 'installing' | 'error';
    version: string | null;
    progress: { percent: number; transferred: number; total: number; bytesPerSecond: number } | null;
    error: 'check' | 'download' | 'integrity' | 'manifest' | 'install' | null;
    errorOperation: 'check' | 'download' | 'install' | null;
    checkedAt: string | null;
}

interface DesktopUpdaterBridge {
    getState(): Promise<DesktopUpdateState>;
    check(): Promise<DesktopUpdateState>;
    download(): Promise<DesktopUpdateState>;
    cancel(): Promise<DesktopUpdateState>;
    install(): Promise<DesktopUpdateState>;
    onState(callback: (state: DesktopUpdateState) => void): () => void;
}

declare global {
    interface Window {
        octopusDesktop?: { updater: DesktopUpdaterBridge };
    }
}

export function useDesktopUpdate() {
    const [state, setState] = useState<DesktopUpdateState | null>(null);
    const [bridgeError, setBridgeError] = useState(false);
    const receive = useCallback((next: DesktopUpdateState) => {
        // 订阅事件可能先于 IPC 快照返回，不能用旧快照回退下载状态。
        setState(previous => !previous || next.revision >= previous.revision ? next : previous);
        setBridgeError(false);
    }, []);

    useEffect(() => {
        const bridge = window.octopusDesktop?.updater;
        if (!bridge) return;
        let active = true;
        const unsubscribe = bridge.onState(next => { if (active) receive(next); });
        void bridge.getState().then(next => { if (active) receive(next); }).catch(() => { if (active) setBridgeError(true); });
        return () => { active = false; unsubscribe(); };
    }, [receive]);

    const run = useCallback(async (action: DesktopUpdateAction) => {
        const bridge = window.octopusDesktop?.updater;
        if (!bridge) return;
        try { receive(await bridge[action]()); }
        catch { setBridgeError(true); }
    }, [receive]);

    return { state, bridgeError, run };
}
