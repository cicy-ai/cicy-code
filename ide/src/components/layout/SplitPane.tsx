import React, { useState } from 'react';

interface SplitPaneProps {
  left: React.ReactNode;
  right: React.ReactNode;
  defaultRightWidth?: number;
  minRight?: number;
}

const SplitPane: React.FC<SplitPaneProps> = ({ left, right, defaultRightWidth = 360, minRight = 200 }) => {
  const [rightW, setRightW] = useState(() => {
    const saved = localStorage.getItem('v2_splitRightW');
    return saved ? parseInt(saved) : defaultRightWidth;
  });
  const [dragging, setDragging] = useState(false);

  const onDragStart = (e: React.MouseEvent) => {
    e.preventDefault();
    setDragging(true);
    const startX = e.clientX;
    const startW = rightW;
    const onMove = (ev: MouseEvent) => {
      const newW = Math.max(minRight, Math.min(window.innerWidth - 300, startW - (ev.clientX - startX)));
      setRightW(newW);
      localStorage.setItem('v2_splitRightW', newW.toString());
    };
    const onUp = () => {
      setDragging(false);
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  };

  return (
    <div className="flex-1 flex relative overflow-hidden">
      {dragging && <div className="fixed inset-0 z-[9999] cursor-col-resize" />}
      <div className="flex-1 min-w-0">{left}</div>
      <div className="w-1 cursor-col-resize hover:bg-blue-500/50 z-20 flex-shrink-0" onMouseDown={onDragStart} />
      <div className="flex-shrink-0" style={{ width: rightW }}>{right}</div>
    </div>
  );
};

export default SplitPane;
