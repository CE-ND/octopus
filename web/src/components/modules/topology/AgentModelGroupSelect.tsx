'use client';

import type { Group } from '@/api/endpoints/group';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

export type AgentModelGroup = Group & { id: number };

export function AgentModelGroupSelect({ id, value, groups, allowed, placeholder, disabled, onChange }: {
    id: string;
    value?: number;
    groups: AgentModelGroup[];
    allowed?: Set<string>;
    placeholder: string;
    disabled: boolean;
    onChange: (value: number) => void;
}) {
    return (
        <Select value={value ? String(value) : '__keep__'} onValueChange={(next) => onChange(next === '__keep__' ? 0 : Number(next))} disabled={disabled}>
            <SelectTrigger id={id} className="w-full rounded-xl bg-background shadow-none">
                <SelectValue placeholder={placeholder} />
            </SelectTrigger>
            <SelectContent className="z-[70] rounded-xl">
                <SelectItem value="__keep__" className="rounded-lg">{placeholder}</SelectItem>
                {groups.map((group) => (
                    <SelectItem key={group.id} value={String(group.id)} disabled={allowed !== undefined && !allowed.has(group.name)} className="rounded-lg">
                        {group.name}
                    </SelectItem>
                ))}
            </SelectContent>
        </Select>
    );
}
