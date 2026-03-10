import React, { useState, useRef, useEffect, ReactNode } from 'react';
import { GripHorizontal, X } from 'lucide-react';
import { Position, Size } from '../types';

interface FloatingPanelProps {
  children: ReactNode;
  title: string;
  initialPosition?: Position;
  initialSize?: Size;
  minSize?: Size;
  onInteractionStart: () => void;
  onInteractionEnd: () => void;
  onChange: (position: Position, size: Size) => void;
  onClose?: () => void;
  headerActions?: ReactNode;
  onDraggingChange?: (isDragging: boolean) => void;
  disableDrag?: boolean;
}

export const FloatingPanel: React.FC<FloatingPanelProps> = ({
  children,
  title,
  initialPosition = { x: 50, y: 50 },
  initialSize = { width: 357, height: 166 },
  minSize = { width: 357, height: 166 },
  onInteractionStart,
  onInteractionEnd,
  onChange,
  onClose,
  headerActions,
  onDraggingChange,
  disableDrag = false
}) => {
  const [position, setPosition] = useState<Position>(initialPosition);
  const [size, setSize] = useState<Size>(initialSize);
  const [isDragging, setIsDragging] = useState(false);
  const [isResizing, setIsResizing] = useState(false);

  // Sync internal state if props change
  useEffect(() => {
    setPosition(initialPosition);
  }, [initialPosition.x, initialPosition.y]);

  useEffect(() => {
    setSize(initialSize);
  }, [initialSize.width, initialSize.height]);

  const panelRef = useRef<HTMLDivElement>(null);
  const dragStartPos = useRef<Position>({ x: 0, y: 0 });
  const resizeStartPos = useRef<Position>({ x: 0, y: 0 });
  const startDims = useRef<{ pos: Position; size: Size }>({ pos: initialPosition, size: initialSize });

  // Helper to get coordinates from either mouse or touch event
  const getClientPos = (e: MouseEvent | TouchEvent | React.MouseEvent | React.TouchEvent) => {
    if ('touches' in e && e.touches.length > 0) {
      return { x: e.touches[0].clientX, y: e.touches[0].clientY };
    } else if ('clientX' in e) {
      return { x: (e as MouseEvent).clientX, y: (e as MouseEvent).clientY };
    }
    return { x: 0, y: 0 };
  };

  // --- Drag Handlers ---
  const handleStartDrag = (e: React.MouseEvent | React.TouchEvent) => {
    if (disableDrag) return;
    // Check if target is a button or inside a button (to allow clicking header actions)
    if ((e.target as HTMLElement).closest('button')) return;
    // Also allow interacting with inputs/selects if we put them in header
    if (['INPUT', 'SELECT', 'OPTION'].includes((e.target as HTMLElement).tagName)) return;

    if (e.cancelable) e.preventDefault(); 
    
    setIsDragging(true);
    onDraggingChange?.(true);
    onInteractionStart();
    const clientPos = getClientPos(e);
    dragStartPos.current = clientPos;
    startDims.current.pos = { ...position };
  };

  // --- Resize Handlers ---
  const handleStartResize = (e: React.MouseEvent | React.TouchEvent) => {
    e.stopPropagation();
    if (e.cancelable) e.preventDefault();

    setIsResizing(true);
    onInteractionStart();
    const clientPos = getClientPos(e);
    resizeStartPos.current = clientPos;
    startDims.current.size = { ...size };
  };

  useEffect(() => {
    const handleMove = (e: MouseEvent | TouchEvent) => {
      const clientPos = getClientPos(e);

      if (isDragging) {
        if(e.cancelable) e.preventDefault(); 
        const dx = clientPos.x - dragStartPos.current.x;
        const dy = clientPos.y - dragStartPos.current.y;
        
        // Boundary checks
        const newX = Math.max(0, Math.min(window.innerWidth - size.width, startDims.current.pos.x + dx));
        const newY = Math.max(0, Math.min(window.innerHeight - size.height, startDims.current.pos.y + dy));

        setPosition({ x: newX, y: newY });
      }

      if (isResizing) {
        if(e.cancelable) e.preventDefault();
        const dx = clientPos.x - resizeStartPos.current.x;
        const dy = clientPos.y - resizeStartPos.current.y;

        const newWidth = Math.max(minSize.width, startDims.current.size.width + dx);
        const newHeight = Math.max(minSize.height, startDims.current.size.height + dy);

        setSize({ width: newWidth, height: newHeight });
      }
    };

    const handleEnd = () => {
      if (isDragging || isResizing) {
        setIsDragging(false);
        setIsResizing(false);
        onDraggingChange?.(false);
        onInteractionEnd();
      }
    };

    if (isDragging || isResizing) {
      window.addEventListener('mousemove', handleMove, { passive: false });
      window.addEventListener('mouseup', handleEnd);
      window.addEventListener('touchmove', handleMove, { passive: false });
      window.addEventListener('touchend', handleEnd);
    }

    return () => {
      window.removeEventListener('mousemove', handleMove);
      window.removeEventListener('mouseup', handleEnd);
      window.removeEventListener('touchmove', handleMove);
      window.removeEventListener('touchend', handleEnd);
    };
  }, [isDragging, isResizing, minSize, onInteractionEnd, position, size]);

  // Trigger onChange only when interaction stops
  useEffect(() => {
    if (!isDragging && !isResizing) {
        onChange(position, size);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isDragging, isResizing]); 

  return (
    <div
      ref={panelRef}
      className="flex flex-col bg-vsc-bg/95 backdrop-blur-md border border-vsc-border shadow-2xl overflow-hidden touch-none"
      style={{
        position: disableDrag ? 'relative' : 'fixed',
        left: disableDrag ? 'auto' : position.x,
        top: disableDrag ? 'auto' : position.y,
        width: disableDrag ? '100%' : size.width,
        height: disableDrag ? '100%' : size.height,
        minHeight: '140px',
        boxShadow: '0 25px 50px -12px rgba(0, 0, 0, 0.5)',
        zIndex: 999999,
        borderRadius: disableDrag ? 0 : '0.5rem',
      }}
    >
      {/* Header / Drag Handle */}
      <div
        className="h-10 bg-vsc-bg-titlebar border-b border-vsc-border flex items-center justify-between px-3 select-none touch-none shrink-0"
        style={{cursor: disableDrag ? 'default' : 'move'}}
        onMouseDown={handleStartDrag}
        onTouchStart={handleStartDrag}
      >
        <div className="flex items-center gap-2 text-vsc-text min-w-0 mr-2">
          {title}
        </div>
        
        <div className="flex items-center gap-1 md:gap-2 shrink-0">
            {headerActions}
            
            {onClose && (
                <button 
                    onClick={(e) => { e.stopPropagation(); onClose(); }}
                    className="p-2 hover:bg-red-500/20 hover:text-red-400 rounded-lg text-vsc-text-secondary transition-colors ml-1 md:ml-2"
                    title="Close Panel"
                >
                    <X size={18} />
                </button>
            )}
        </div>
      </div>

      {/* Content Area */}
      <div className="flex-1 overflow-hidden relative flex flex-col">
        {children}
      </div>
    </div>
  );
};