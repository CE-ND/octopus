'use client';

import { useCallback, useLayoutEffect, useRef } from 'react';
import { motion, useReducedMotion } from 'motion/react';
import { AgentConfigResizeHandle } from './AgentConfigResize';

const EDITOR_ENTER = { scale: 0.97, y: 5 } as const;
const EDITOR_VISIBLE = { scale: 1, y: 0 } as const;
const EDITOR_TRANSITION = { type: 'spring', stiffness: 380, damping: 24, mass: 0.7 } as const;

type EditorPosition = {
    scrollTop: number;
    scrollLeft: number;
    selectionStart: number;
    selectionEnd: number;
    selectionDirection: 'forward' | 'backward' | 'none';
};

type AgentConfigTextEditorProps = {
    fileKey: string;
    active: boolean;
    label: string;
    value: string;
    disabled: boolean;
    placeholder: string;
    onChange: (content: string) => void;
};

/** 文件切换时轻微缩放弹出，编辑与滚动不会重新触发动效。 */
export function AgentConfigTextEditor({ fileKey, active, label, value, disabled, placeholder, onChange }: AgentConfigTextEditorProps) {
    const reducedMotion = useReducedMotion();
    const editorRef = useRef<HTMLTextAreaElement>(null);
    const positionsRef = useRef(new Map<string, EditorPosition>());

    const rememberPosition = useCallback((editor: HTMLTextAreaElement) => {
        positionsRef.current.set(fileKey, {
            scrollTop: editor.scrollTop,
            scrollLeft: editor.scrollLeft,
            selectionStart: editor.selectionStart,
            selectionEnd: editor.selectionEnd,
            selectionDirection: editor.selectionDirection,
        });
    }, [fileKey]);

    const bindEditor = useCallback((editor: HTMLTextAreaElement | null) => {
        // 在节点替换前保存最终选区和滚动位置，避免切换文件时丢失。
        if (!editor && editorRef.current) rememberPosition(editorRef.current);
        editorRef.current = editor;
    }, [rememberPosition]);

    useLayoutEffect(() => {
        const editor = editorRef.current;
        if (!editor) return;
        const position = positionsRef.current.get(fileKey);
        // 在首帧之前还原编辑位置；设置选区不会抢走文件选择器的焦点。
        editor.setSelectionRange(
            Math.min(position?.selectionStart ?? 0, editor.value.length),
            Math.min(position?.selectionEnd ?? 0, editor.value.length),
            position?.selectionDirection ?? 'none',
        );
        editor.scrollTop = position?.scrollTop ?? 0;
        editor.scrollLeft = position?.scrollLeft ?? 0;
    }, [fileKey]);

    return (
        <motion.div
            key={fileKey}
            data-slot="agent-config-editor-shell"
            initial={reducedMotion || !active ? false : EDITOR_ENTER}
            animate={EDITOR_VISIBLE}
            transition={reducedMotion ? { duration: 0 } : EDITOR_TRANSITION}
            style={{ transformOrigin: 'top center' }}
            className="group/editor relative min-h-0 flex-1 rounded-xl"
        >
            <textarea
                ref={bindEditor}
                data-slot="agent-config-editor"
                data-file-key={fileKey}
                aria-label={label}
                value={value}
                onChange={(event) => {
                    rememberPosition(event.currentTarget);
                    onChange(event.currentTarget.value);
                }}
                onScroll={(event) => rememberPosition(event.currentTarget)}
                onSelect={(event) => rememberPosition(event.currentTarget)}
                onBlur={(event) => rememberPosition(event.currentTarget)}
                disabled={disabled}
                placeholder={placeholder}
                spellCheck={false}
                wrap="off"
                className="block h-full min-h-0 w-full resize-none overflow-auto overscroll-contain rounded-xl border border-border bg-background p-4 pb-8 pr-9 font-mono text-xs leading-relaxed text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
            <AgentConfigResizeHandle disabled={disabled} />
        </motion.div>
    );
}
