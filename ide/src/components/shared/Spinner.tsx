import React from 'react';
import { Loader2 } from 'lucide-react';

const Spinner: React.FC<{ size?: number }> = ({ size = 48 }) => (
  <div className="bg-vsc-bg w-screen h-screen flex items-center justify-center">
    <Loader2 size={size} className="text-vsc-accent animate-spin" />
  </div>
);

export default Spinner;
