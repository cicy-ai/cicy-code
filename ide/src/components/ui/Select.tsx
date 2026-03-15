import React, { useState, useRef, useEffect } from 'react';
import { ChevronDown, Search } from 'lucide-react';

export interface SelectOption {
  value: string;
  label: string;
  sub?: string;
}

interface Props {
  options: SelectOption[];
  value?: string;
  onChange: (value: string) => void;
  placeholder?: string;
  searchable?: boolean;
  className?: string;
}

export default function Select({ options, value, onChange, placeholder = 'Select...', searchable = false, className = '' }: Props) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const ref = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const handler = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false); };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  useEffect(() => { if (open && searchable) setTimeout(() => inputRef.current?.focus(), 0); }, [open, searchable]);

  const filtered = search ? options.filter(o => o.label.toLowerCase().includes(search.toLowerCase()) || o.sub?.toLowerCase().includes(search.toLowerCase())) : options;
  const selected = options.find(o => o.value === value);

  return (
    <div ref={ref} className={`relative ${className}`}>
      <button
        onClick={() => { setOpen(!open); setSearch(''); }}
        className="w-full flex items-center gap-2 text-sm bg-[var(--vsc-bg)] border border-[var(--vsc-border)] rounded px-2 py-1.5 text-left hover:border-zinc-500 transition-colors cursor-pointer"
      >
        <span className={`flex-1 truncate ${selected ? 'text-zinc-300' : 'text-zinc-500'}`}>
          {selected ? selected.label : placeholder}
        </span>
        <ChevronDown className={`w-3 h-3 text-zinc-500 transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>

      {open && (
        <div className="absolute z-50 top-full left-0 right-0 mt-1 bg-[#1e1e1e] border border-[var(--vsc-border)] rounded-md shadow-xl overflow-hidden">
          {searchable && (
            <div className="flex items-center gap-1.5 px-2 py-1.5 border-b border-[var(--vsc-border)]">
              <Search className="w-3 h-3 text-zinc-500" />
              <input
                ref={inputRef}
                value={search}
                onChange={e => setSearch(e.target.value)}
                className="flex-1 text-sm bg-transparent text-zinc-300 outline-none placeholder-zinc-600"
                placeholder="Search..."
              />
            </div>
          )}
          <div className="max-h-48 overflow-y-auto">
            {filtered.length ? filtered.map(o => (
              <div
                key={o.value}
                onClick={() => { onChange(o.value); setOpen(false); setSearch(''); }}
                className={`flex items-center gap-2 px-2 py-1.5 text-sm cursor-pointer transition-colors ${o.value === value ? 'bg-blue-500/10 text-blue-400' : 'text-zinc-300 hover:bg-white/[0.06]'}`}
              >
                <span className="truncate">{o.label}</span>
                {o.sub && <span className="text-zinc-600 text-[10px] ml-auto">{o.sub}</span>}
              </div>
            )) : (
              <div className="px-2 py-3 text-sm text-zinc-600 text-center">No results</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
