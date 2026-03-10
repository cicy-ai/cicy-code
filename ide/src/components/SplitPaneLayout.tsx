import React, { useState, useRef, useEffect } from 'react';
import { GripVertical } from 'lucide-react';

interface SplitPaneLayoutProps {
  children: [React.ReactNode, React.ReactNode];
  direction?: 'horizontal' | 'vertical';
  initialSplit?: number; // 0-100
  minSize?: number; // pixels
  onInteractionStart?: () => void;
  onInteractionEnd?: () => void;
}

export const SplitPaneLayout: React.FC<SplitPaneLayoutProps> = ({
  children,
  direction = 'horizontal',
  initialSplit = 50,
  minSize = 100,
  onInteractionStart,
  onInteractionEnd
}) => {
  const [split, setSplit] = useState(initialSplit);
  const [isDragging, setIsDragging] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  const handleMouseDown = (e: React.MouseEvent) => {
    e.preventDefault();
    setIsDragging(true);
    onInteractionStart?.();
  };

  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      if (!isDragging || !containerRef.current) return;

      const rect = containerRef.current.getBoundingClientRect();
      let newSplit: number;

      if (direction === 'horizontal') {
        const x = e.clientX - rect.left;
        newSplit = (x / rect.width) * 100;
      } else {
        const y = e.clientY - rect.top;
        newSplit = (y / rect.height) * 100;
      }

      // Apply min size constraints
      const minPercent = (minSize / (direction === 'horizontal' ? rect.width : rect.height)) * 100;
      newSplit = Math.max(minPercent, Math.min(100 - minPercent, newSplit));

      setSplit(newSplit);
    };

    const handleMouseUp = () => {
      if (isDragging) {
        setIsDragging(false);
        onInteractionEnd?.();
      }
    };

    if (isDragging) {
      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
    }

    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
  }, [isDragging, direction, minSize, onInteractionEnd]);

  const isHorizontal = direction === 'horizontal';

  return (
    <div
      ref={containerRef}
      className={`relative w-full h-full flex ${isHorizontal ? 'flex-row' : 'flex-col'}`}
    >
      {/* First Pane */}
      <div
        style={{
          [isHorizontal ? 'width' : 'height']: `${split}%`,
        }}
        className="relative overflow-hidden"
      >
        {children[0]}
      </div>

      {/* Divider */}
      <div
        className={`relative flex-shrink-0 bg-vsc-border hover:bg-vsc-accent transition-colors ${
          isHorizontal ? 'w-1 cursor-col-resize' : 'h-1 cursor-row-resize'
        } ${isDragging ? 'bg-vsc-accent' : ''} group`}
        onMouseDown={handleMouseDown}
      >
        <div
          className={`absolute ${
            isHorizontal
              ? 'top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2'
              : 'left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2'
          } opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none`}
        >
          <div className={`bg-vsc-bg rounded-full p-1 ${isHorizontal ? '' : 'rotate-90'}`}>
            <GripVertical size={16} className="text-white" />
          </div>
        </div>
      </div>

      {/* Second Pane */}
      <div
        style={{
          [isHorizontal ? 'width' : 'height']: `${100 - split}%`,
        }}
        className="relative overflow-hidden"
      >
        {children[1]}
      </div>
    </div>
  );
};
