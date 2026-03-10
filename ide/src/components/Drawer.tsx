import React, { useState } from 'react';

const CACHE_KEY = 'drawerWidth';

const Drawer: React.FC<{ open: boolean; onClose: () => void; children?: React.ReactNode }> = ({ open, onClose, children }) => {
  const [width, setWidth] = useState(() => {
    const saved = localStorage.getItem(CACHE_KEY);
    return saved ? parseInt(saved) : Math.round(window.innerWidth * 0.45);
  });
  const [dragging, setDragging] = useState(false);

  const onDragStart = (e: React.MouseEvent) => {
    e.preventDefault();
    setDragging(true);
    const startX = e.clientX;
    const startW = width;
    const onMove = (ev: MouseEvent) => {
      const newW = Math.max(200, Math.min(window.innerWidth * 0.9, startW - (ev.clientX - startX)));
      setWidth(newW);
    };
    const onUp = () => {
      setDragging(false);
      setWidth(w => { localStorage.setItem(CACHE_KEY, String(w)); return w; });
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  };

  return (
    <>
      {dragging && <div className="fixed inset-0 cursor-col-resize" style={{zIndex: 999999}} />}
      <div
        id="drawer-right"
        className="fixed top-0 right-0 bottom-0 transition-transform duration-300 border-l border-vsc-border"
        style={{ width, transform: open ? 'translateX(0)' : 'translateX(100%)', zIndex: 100000 }}
      >
        <div
          className="absolute top-0 bottom-0 left-0 w-1 hover:bg-vsc-accent cursor-col-resize z-10"
          onMouseDown={onDragStart}
        />
        <div className="w-full h-full overflow-hidden">
          {children || <div className="w-full h-full bg-vsc-bg" />}
        </div>
      </div>
    </>
  );
};

export default Drawer;
