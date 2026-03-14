import React, { useEffect } from 'react';

const Toast: React.FC<{ message: string | null; onDismiss: () => void }> = ({ message, onDismiss }) => {
  useEffect(() => {
    if (message) {
      const t = setTimeout(onDismiss, 4000);
      return () => clearTimeout(t);
    }
  }, [message]);

  if (!message) return null;

  const isError = message.startsWith('Failed') || message.startsWith('Error');
  return (
    <div
      className={`fixed top-4 left-1/2 -translate-x-1/2 px-4 py-2.5 text-white text-sm font-medium rounded-lg shadow-lg ${isError ? 'bg-red-500/90' : 'bg-emerald-500/90'}`}
      style={{ zIndex: 999999999 }}
    >
      {message}
    </div>
  );
};

export default Toast;
