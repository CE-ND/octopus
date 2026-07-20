'use client';

import { useMemo, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { AlertTriangle, Calendar, Clock, Database, Download, Eraser, FileArchive, FileWarning, ScrollText, Trash2, Upload } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
    AlertDialogTrigger,
} from '@/components/ui/alert-dialog';
import { toast } from '@/components/common/Toast';
import { SettingKey, useExportDB, useImportDB } from '@/api/endpoints/setting';
import { useClearLogContent, useClearLogs } from '@/api/endpoints/log';
import { SettingCard, SettingRow, SettingSection, useSettingField, useSettingToggle } from './shared';

function formatBytes(bytes: number) {
    if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    const unitIndex = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
    const value = bytes / (1024 ** unitIndex);
    return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: unitIndex === 0 ? 0 : 1 }).format(value)} ${units[unitIndex]}`;
}

export function SettingData() {
    const t = useTranslations('setting');

    // 历史日志与统计持久化
    const logEnabled = useSettingToggle(SettingKey.RelayLogKeepEnabled);
    const fullLogContent = useSettingToggle(SettingKey.RelayLogFullContentEnabled);
    const badResponsesDump = useSettingToggle(SettingKey.DumpBadResponsesEnabled);
    const keepPeriod = useSettingField(SettingKey.RelayLogKeepPeriod);
    const statsInterval = useSettingField(SettingKey.StatsSaveInterval);
    const clearLogContent = useClearLogContent();
    const clearLogs = useClearLogs();
    const logMaintenancePending = clearLogContent.isPending || clearLogs.isPending;

    // 备份导出/导入
    const exportDB = useExportDB();
    const importDB = useImportDB();

    const [includeStats, setIncludeStats] = useState(true);
    // 常规导出固定 JSON（可导入恢复）；含日志导出为 ZIP 流式归档，单独成按钮
    const [exportingKind, setExportingKind] = useState<'json' | 'logs' | null>(null);

    const [file, setFile] = useState<File | null>(null);
    const fileInputRef = useRef<HTMLInputElement | null>(null);

    const rowsAffected = importDB.data?.rows_affected ?? null;
    const rowsAffectedList = useMemo(() => {
        if (!rowsAffected) return [];
        return Object.entries(rowsAffected)
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([k, v]) => ({ table: k, count: v }));
    }, [rowsAffected]);

    const handleClearLogs = () => {
        clearLogs.mutate(undefined, {
            onSuccess: () => toast.success(t('log.clearAll.success')),
            onError: () => toast.error(t('log.clearAll.failed')),
        });
    };

    const handleClearLogContent = () => {
        clearLogContent.mutate(undefined, {
            onSuccess: (result) => toast.success(result.database_bytes_before > 0
                ? t('log.clearContent.successCompacted', {
                    count: result.rows_affected,
                    before: formatBytes(result.database_bytes_before),
                    after: formatBytes(result.database_bytes_after),
                    reclaimed: formatBytes(result.reclaimed_bytes),
                })
                : t('log.clearContent.success', { count: result.rows_affected })),
            onError: () => toast.error(t('log.clearContent.failed')),
        });
    };

    const onImport = async () => {
        if (!file) {
            toast.error(t('backup.import.noFile'));
            return;
        }
        // accept 属性只约束选择器默认过滤，仍可手动选任意文件，导入前再校验一次
        if (file.type !== 'application/json' && !file.name.toLowerCase().endsWith('.json')) {
            toast.error(t('backup.import.invalidFileType'));
            if (fileInputRef.current) fileInputRef.current.value = '';
            setFile(null);
            return;
        }
        try {
            await importDB.mutateAsync(file);
            toast.success(t('backup.import.success'));
            if (fileInputRef.current) fileInputRef.current.value = '';
            setFile(null);
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.import.failed'));
        }
    };

    const onExport = async (kind: 'json' | 'logs') => {
        setExportingKind(kind);
        try {
            await exportDB.mutateAsync(kind === 'logs'
                ? { include_logs: true, include_stats: includeStats, format: 'zip' }
                : { include_logs: false, include_stats: includeStats, format: 'json' });
            toast.success(t('backup.export.success'));
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.export.failed'));
        } finally {
            setExportingKind(null);
        }
    };

    return (
        <SettingCard icon={Database} title={t('data.title')}>
            {/* 统计保存周期 */}
            <SettingRow icon={Clock} label={t('statsSaveInterval.label')}>
                <Input
                    type="number"
                    value={statsInterval.value}
                    onChange={(e) => statsInterval.setValue(e.target.value)}
                    onBlur={statsInterval.save}
                    placeholder={t('statsSaveInterval.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            {/* 历史日志 */}
            <SettingSection title={t('log.title')} />
            <SettingRow icon={ScrollText} label={t('log.enabled.label')}>
                <Switch checked={logEnabled.enabled} onCheckedChange={logEnabled.toggle} />
            </SettingRow>
            <SettingRow icon={FileWarning} label={t('log.fullContent.label')} tooltip={t('log.fullContent.description')}>
                <Switch checked={fullLogContent.enabled} onCheckedChange={fullLogContent.toggle} />
            </SettingRow>
            <SettingRow icon={AlertTriangle} label={t('log.badResponsesDump.label')} tooltip={t('log.badResponsesDump.description')}>
                <Switch checked={badResponsesDump.enabled} onCheckedChange={badResponsesDump.toggle} />
            </SettingRow>
            <SettingRow icon={Calendar} label={t('log.keepPeriod.label')}>
                <Input
                    type="number"
                    value={keepPeriod.value}
                    onChange={(e) => keepPeriod.setValue(e.target.value)}
                    onBlur={keepPeriod.save}
                    placeholder={t('log.keepPeriod.placeholder')}
                    className="w-48 rounded-xl"
                    disabled={!logEnabled.enabled}
                />
            </SettingRow>
            <SettingRow icon={Eraser} label={t('log.clearContent.label')} tooltip={t('log.clearContent.description')}>
                <AlertDialog>
                    <AlertDialogTrigger asChild>
                        <Button variant="outline" size="sm" disabled={logMaintenancePending} className="rounded-xl">
                            <Eraser className="size-4" />
                            {clearLogContent.isPending ? t('log.clearContent.clearing') : t('log.clearContent.button')}
                        </Button>
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                        <AlertDialogHeader>
                            <AlertDialogTitle>{t('log.clearContent.confirmTitle')}</AlertDialogTitle>
                            <AlertDialogDescription>{t('log.clearContent.confirmDescription')}</AlertDialogDescription>
                        </AlertDialogHeader>
                        <AlertDialogFooter>
                            <AlertDialogCancel>{t('log.clearContent.confirmCancel')}</AlertDialogCancel>
                            <AlertDialogAction
                                onClick={handleClearLogContent}
                                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                            >
                                {t('log.clearContent.confirmAction')}
                            </AlertDialogAction>
                        </AlertDialogFooter>
                    </AlertDialogContent>
                </AlertDialog>
            </SettingRow>
            <SettingRow icon={Trash2} label={t('log.clearAll.label')} tooltip={t('log.clearAll.description')}>
                <AlertDialog>
                    <AlertDialogTrigger asChild>
                        <Button variant="destructive" size="sm" disabled={logMaintenancePending} className="rounded-xl">
                            <Trash2 className="size-4" />
                            {clearLogs.isPending ? t('log.clearAll.clearing') : t('log.clearAll.button')}
                        </Button>
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                        <AlertDialogHeader>
                            <AlertDialogTitle>{t('log.clearAll.confirmTitle')}</AlertDialogTitle>
                            <AlertDialogDescription>{t('log.clearAll.confirmDescription')}</AlertDialogDescription>
                        </AlertDialogHeader>
                        <AlertDialogFooter>
                            <AlertDialogCancel>{t('log.clearAll.confirmCancel')}</AlertDialogCancel>
                            <AlertDialogAction
                                onClick={handleClearLogs}
                                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                            >
                                {t('log.clearAll.confirmAction')}
                            </AlertDialogAction>
                        </AlertDialogFooter>
                    </AlertDialogContent>
                </AlertDialog>
            </SettingRow>

            {/* 备份导出 */}
            <SettingSection title={t('backup.export.title')} />
            <div className="space-y-3">
                <div className="flex items-center justify-between gap-4">
                    <div className="text-sm text-muted-foreground">{t('backup.export.includeStats')}</div>
                    <Switch checked={includeStats} onCheckedChange={setIncludeStats} />
                </div>

                <Button
                    type="button"
                    variant="outline"
                    className="w-full rounded-xl"
                    onClick={() => onExport('json')}
                    disabled={exportDB.isPending}
                >
                    <Download className="size-4" />
                    {exportingKind === 'json' ? t('backup.export.exporting') : t('backup.export.button')}
                </Button>

                {/* 含日志归档：数据量大，ZIP 流式写入，仅供留存，无法导入恢复 */}
                <Button
                    type="button"
                    variant="outline"
                    className="w-full rounded-xl"
                    onClick={() => onExport('logs')}
                    disabled={exportDB.isPending}
                >
                    <FileArchive className="size-4" />
                    {exportingKind === 'logs' ? t('backup.export.exporting') : t('backup.export.withLogsButton')}
                </Button>
                <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
                    <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-destructive" />
                    {t('backup.export.withLogsWarning')}
                </p>
            </div>

            {/* 备份导入 */}
            <SettingSection title={t('backup.import.title')} />
            <div className="space-y-3">
                <Input
                    ref={fileInputRef}
                    type="file"
                    accept="application/json,.json"
                    onChange={(e) => setFile(e.target.files?.[0] ?? null)}
                    className="rounded-xl"
                />

                <Button
                    type="button"
                    variant="destructive"
                    className="w-full rounded-xl"
                    onClick={onImport}
                    disabled={importDB.isPending}
                >
                    <Upload className="size-4" />
                    {importDB.isPending ? t('backup.import.importing') : t('backup.import.button')}
                </Button>

                {rowsAffectedList.length > 0 && (
                    <div className="mt-2 space-y-1">
                        <div className="text-xs font-semibold text-card-foreground">{t('backup.import.result')}</div>
                        <div className="grid grid-cols-2 gap-1 text-xs text-muted-foreground">
                            {rowsAffectedList.map((it) => (
                                <div key={it.table} className="flex justify-between gap-2">
                                    <span className="truncate">{it.table}</span>
                                    <span className="tabular-nums">{it.count}</span>
                                </div>
                            ))}
                        </div>
                    </div>
                )}
            </div>
        </SettingCard>
    );
}
