'use client';

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react';
import { motion, useReducedMotion } from 'motion/react';
import { useTranslations } from 'next-intl';
import { cn } from '@/lib/utils';

type DialogSize = { width: number; height: number };
type ResizeGesture = DialogSize & { pointerId: number; startX: number; startY: number };
type ResizeAnimation = { size: DialogSize | null; requested: DialogSize | null; target: DialogSize | null; frame: number | null; time: number };
type ResizeControls = {
    dragging: boolean;
    start: (event: PointerEvent<HTMLButtonElement>) => void;
    move: (event: PointerEvent<HTMLButtonElement>) => void;
    end: (event: PointerEvent<HTMLButtonElement>) => void;
    keyDown: (event: KeyboardEvent<HTMLButtonElement>) => void;
};

const ResizeContext = createContext<ResizeControls | null>(null);
export const AgentConfigResizeProvider = ResizeContext.Provider;

const RESIZE_RESPONSE_MS = 32;
const PRESS_INSET = { width: 8, height: 6 } as const;
const HANDLE_TRANSITION = { type: 'spring', stiffness: 680, damping: 32, mass: 0.4 } as const;

function constrainSize(size: DialogSize): DialogSize {
    const maxWidth = Math.min(1040, Math.max(0, window.innerWidth - 32));
    const maxHeight = Math.max(0, window.innerHeight - 32);
    const minWidth = Math.min(560, maxWidth);
    const minHeight = Math.min(460, maxHeight);
    return {
        width: Math.round(Math.min(maxWidth, Math.max(minWidth, size.width))),
        height: Math.round(Math.min(maxHeight, Math.max(minHeight, size.height))),
    };
}

export function useAgentConfigResizeState() {
    const reducedMotion = useReducedMotion();
    const [size, setSize] = useState<DialogSize>();
    const [dragging, setDragging] = useState(false);
    const gestureRef = useRef<ResizeGesture | null>(null);
    const animationRef = useRef<ResizeAnimation>({ size: null, requested: null, target: null, frame: null, time: 0 });

    const cancelResize = useCallback(() => {
        const animation = animationRef.current;
        if (animation.frame !== null) window.cancelAnimationFrame(animation.frame);
        animation.frame = null;
    }, []);

    const resizeTo = useCallback((requested: DialogSize, immediate = false) => {
        const animation = animationRef.current;
        const requestedSize = constrainSize(requested);
        animation.requested = requestedSize;
        // 用实际宽高的轻微收缩表现按压，正文保持原字号，避免整体 scale 重采样文字。
        const target = gestureRef.current && !reducedMotion ? constrainSize({
            width: requestedSize.width - PRESS_INSET.width,
            height: requestedSize.height - PRESS_INSET.height,
        }) : requestedSize;
        animation.target = target;
        if (immediate || reducedMotion || !animation.size) {
            cancelResize();
            animation.size = target;
            setSize(target);
            return;
        }
        if (animation.frame !== null) return;
        animation.time = performance.now();

        const tick = (time: number) => {
            const current = animation.size;
            const destination = animation.target;
            if (!current || !destination) return;
            const elapsed = Math.min(64, Math.max(0, time - animation.time));
            animation.time = time;
            // 短缓动持续追随最新指针位置，不越界回弹；实际宽高取整，保持文字清晰。
            const blend = 1 - Math.exp(-elapsed / RESIZE_RESPONSE_MS);
            const next = {
                width: current.width + (destination.width - current.width) * blend,
                height: current.height + (destination.height - current.height) * blend,
            };
            const settled = Math.abs(destination.width - next.width) < 0.25 && Math.abs(destination.height - next.height) < 0.25;
            animation.size = settled ? destination : next;
            const displayed = constrainSize(animation.size);
            setSize(previous => previous?.width === displayed.width && previous.height === displayed.height ? previous : displayed);
            animation.frame = settled ? null : window.requestAnimationFrame(tick);
        };

        animation.frame = window.requestAnimationFrame(tick);
    }, [cancelResize, reducedMotion]);

    useEffect(() => cancelResize, [cancelResize]);

    const start = useCallback((event: PointerEvent<HTMLButtonElement>) => {
        if (event.button !== 0 || !event.isPrimary || gestureRef.current) return;
        const panel = event.currentTarget.closest<HTMLElement>('[role="dialog"]');
        if (!panel) return;
        event.preventDefault();
        event.stopPropagation();
        const current = constrainSize({ width: panel.offsetWidth, height: panel.offsetHeight });
        resizeTo(current, true);
        gestureRef.current = { ...current, pointerId: event.pointerId, startX: event.clientX, startY: event.clientY };
        event.currentTarget.setPointerCapture(event.pointerId);
        setDragging(true);
        resizeTo(current);
    }, [resizeTo]);

    const move = useCallback((event: PointerEvent<HTMLButtonElement>) => {
        const gesture = gestureRef.current;
        if (!gesture || gesture.pointerId !== event.pointerId) return;
        event.preventDefault();
        event.stopPropagation();
        // 模态框居中布局，宽高各增加两倍指针位移，右下角才能跟随鼠标。
        resizeTo({
            width: gesture.width + (event.clientX - gesture.startX) * 2,
            height: gesture.height + (event.clientY - gesture.startY) * 2,
        });
    }, [resizeTo]);

    const end = useCallback((event: PointerEvent<HTMLButtonElement>) => {
        const gesture = gestureRef.current;
        if (!gesture || gesture.pointerId !== event.pointerId) return;
        gestureRef.current = null;
        setDragging(false);
        const requested = animationRef.current.requested;
        if (requested) resizeTo(requested);
        if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    }, [resizeTo]);

    const keyDown = useCallback((event: KeyboardEvent<HTMLButtonElement>) => {
        if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) return;
        const panel = event.currentTarget.closest<HTMLElement>('[role="dialog"]');
        if (!panel) return;
        event.preventDefault();
        event.stopPropagation();
        const step = event.shiftKey ? 48 : 16;
        const current = animationRef.current.requested ?? { width: panel.offsetWidth, height: panel.offsetHeight };
        resizeTo({
            width: current.width + (event.key === 'ArrowRight' ? step : event.key === 'ArrowLeft' ? -step : 0),
            height: current.height + (event.key === 'ArrowDown' ? step : event.key === 'ArrowUp' ? -step : 0),
        });
    }, [resizeTo]);

    useEffect(() => {
        const fitWindow = () => {
            const current = animationRef.current.requested;
            if (current) resizeTo(current, true);
        };
        window.addEventListener('resize', fitWindow);
        return () => window.removeEventListener('resize', fitWindow);
    }, [resizeTo]);

    const controls = useMemo(() => ({ dragging, start, move, end, keyDown }), [dragging, start, move, end, keyDown]);
    return { size, controls };
}

export function AgentConfigResizeHandle({ disabled = false }: { disabled?: boolean }) {
    const controls = useContext(ResizeContext);
    const reducedMotion = useReducedMotion();
    const t = useTranslations('topology');
    if (!controls) return null;

    return (
        <button
            type="button"
            data-slot="agent-config-resize-handle"
            data-dragging={controls.dragging}
            aria-label={t('resizeEditor')}
            title={t('resizeEditorHint')}
            disabled={disabled}
            onPointerDown={controls.start}
            onPointerMove={controls.move}
            onPointerUp={controls.end}
            onPointerCancel={controls.end}
            onLostPointerCapture={controls.end}
            onKeyDown={controls.keyDown}
            className={cn(
                'absolute bottom-0 right-0 size-9 cursor-se-resize touch-none select-none rounded-br-xl rounded-tl-lg text-foreground/75 opacity-0 transition-[opacity,color] duration-150 [transition-delay:2500ms,0ms] hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none',
                'group-hover/editor:opacity-100 group-hover/editor:[transition-delay:0ms,0ms] focus-visible:opacity-100 focus-visible:[transition-delay:0ms,0ms]',
                controls.dragging && 'text-primary opacity-100 [transition-delay:0ms,0ms]',
            )}
        >
            <motion.svg
                viewBox="0 0 28 28"
                className="pointer-events-none absolute bottom-0 right-0 size-7"
                aria-hidden="true"
                initial={false}
                animate={{ scale: controls.dragging && !reducedMotion ? 0.94 : 1 }}
                transition={reducedMotion ? { duration: 0 } : HANDLE_TRANSITION}
                style={{ transformOrigin: 'bottom right' }}
            >
                {/* 描边向内加厚，外沿与输入框的 12px 圆角边框重合。 */}
                <path d="M 8 25 H 16 A 9 9 0 0 0 25 16 V 8" fill="none" stroke="currentColor" strokeWidth="6" strokeLinecap="round" />
            </motion.svg>
        </button>
    );
}
