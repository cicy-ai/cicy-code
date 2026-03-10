import React, { useState, useRef, useEffect } from 'react';
import { Mic, Move } from 'lucide-react';
import { Position } from '../types';

interface VoiceFloatingButtonProps {
  initialPosition: Position;
  onPositionChange: (pos: Position) => void;
  onRecordStart: () => void;
  onRecordEnd: (shouldSend: boolean) => void;
  isRecordingExternal: boolean;
  disabled?: boolean;
}

export const VoiceFloatingButton: React.FC<VoiceFloatingButtonProps> = ({
  initialPosition,
  onPositionChange,
  onRecordStart,
  onRecordEnd,
  isRecordingExternal,
  disabled = false
}) => {
  const [position, setPosition] = useState<Position>(initialPosition);
  const [isDragging, setIsDragging] = useState(false);
  const [isPressed, setIsPressed] = useState(false);
  const [dragMode, setDragMode] = useState(false); 
  
  const dragStartPos = useRef<Position>({ x: 0, y: 0 });
  const startPos = useRef<Position>({ x: 0, y: 0 });
  const stopTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const DRAG_THRESHOLD = 30;

  useEffect(() => {
    setPosition(initialPosition);
  }, [initialPosition.x, initialPosition.y]);

  const getClientPos = (e: MouseEvent | TouchEvent | React.MouseEvent | React.TouchEvent) => {
    if ('touches' in e && e.touches.length > 0) {
      return { x: e.touches[0].clientX, y: e.touches[0].clientY };
    } else if ('clientX' in e) {
      return { x: (e as MouseEvent).clientX, y: (e as MouseEvent).clientY };
    }
    return { x: 0, y: 0 };
  };

  const handleStart = (e: React.MouseEvent | React.TouchEvent) => {
    if (disabled) return;
    // Clear any pending stop timers if user taps quickly again
    if (stopTimerRef.current) {
        clearTimeout(stopTimerRef.current);
        stopTimerRef.current = null;
    }

    if (e.cancelable) e.preventDefault();
    
    const clientPos = getClientPos(e);
    dragStartPos.current = clientPos;
    startPos.current = { ...position };
    
    setIsPressed(true);
    setIsDragging(true);
    setDragMode(false);
    
    // Start recording immediately
    onRecordStart();
  };

  useEffect(() => {
    const handleMove = (e: MouseEvent | TouchEvent) => {
      if (!isDragging) return;
      if (e.cancelable) e.preventDefault();

      const clientPos = getClientPos(e);
      const dx = clientPos.x - dragStartPos.current.x;
      const dy = clientPos.y - dragStartPos.current.y;
      
      // Check if we passed the drag threshold
      if (!dragMode && (Math.abs(dx) > DRAG_THRESHOLD || Math.abs(dy) > DRAG_THRESHOLD)) {
        setDragMode(true);
        // User started dragging, effectively cancelling the voice command intention
        // We trigger end with false immediately so app knows to discard
        onRecordEnd(false); 
      }

      // Constrain to window
      const newX = Math.max(0, Math.min(window.innerWidth - 96, startPos.current.x + dx));
      const newY = Math.max(0, Math.min(window.innerHeight - 96, startPos.current.y + dy));

      setPosition({ x: newX, y: newY });
    };

    const handleEnd = () => {
      if (isDragging) {
        setIsDragging(false);
        setIsPressed(false);
        
        if (dragMode) {
            // If we were dragging, we already cancelled the recording logic in handleMove
            // Just update position
            onPositionChange(position);
            setDragMode(false);
        } else {
            // Normal release - Wait 1s before stopping to capture tail end of audio
            stopTimerRef.current = setTimeout(() => {
                onRecordEnd(true); // true = send it
            }, 1000);
        }
      }
    };

    if (isDragging) {
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
  }, [isDragging, dragMode, position, onRecordEnd, onPositionChange]);

  return (
    <div
      className="fixed z-[50] touch-none select-none"
      style={{
        left: position.x,
        top: position.y,
      }}
      onMouseDown={handleStart}
      onTouchStart={handleStart}
    >
      {/* Visual Ripple Effect Container */}
      <div className="relative w-24 h-24 flex items-center justify-center">
        
        {/* Outer Ripple Rings (Visible when pressed and not dragging) */}
        {isPressed && !dragMode && (
          <>
            <div className="absolute inset-0 rounded-full bg-red-500/30 animate-ping delay-75"></div>
            <div className="absolute -inset-4 rounded-full border-2 border-red-500/20 animate-[ping_2s_cubic-bezier(0,0,0.2,1)_infinite]"></div>
            <div className="absolute -inset-8 rounded-full border border-red-500/10 animate-[ping_3s_cubic-bezier(0,0,0.2,1)_infinite]"></div>
          </>
        )}

        {/* Main Button Circle */}
        <div 
          className={`relative z-10 w-24 h-24 rounded-full flex items-center justify-center shadow-2xl backdrop-blur-md border border-white/10 transition-all duration-300 ease-out ${
            dragMode 
              ? 'bg-vsc-button/80 scale-90 cursor-move' 
              : isPressed 
                ? 'bg-red-600 shadow-[0_0_50px_rgba(220,38,38,0.6)] scale-110 ring-4 ring-white/20' 
                : 'bg-vsc-bg-secondary/90 hover:bg-vsc-bg-active/90 cursor-pointer scale-100'
          }`}
        >
          {dragMode ? (
              <Move size={32} className="text-white" />
          ) : (
              <Mic size={40} className={`transition-all duration-200 ${isPressed ? 'text-white scale-110' : 'text-vsc-text'}`} />
          )}
        </div>
      </div>

      {/* Label/Hint */}
      <div className={`absolute -bottom-10 left-1/2 -translate-x-1/2 text-sm font-bold whitespace-nowrap px-3 py-1.5 rounded-full bg-black/70 text-white backdrop-blur-sm transition-opacity duration-200 ${isPressed ? 'opacity-100' : 'opacity-0'}`}>
        {dragMode ? "Positioning..." : "Recording..."}
      </div>
    </div>
  );
};