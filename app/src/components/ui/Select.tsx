import React, { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ChevronDown, MoreHorizontal, Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';

export interface SelectOptionAction {
  id: string;
  label: string;
  icon?: React.ReactNode;
  danger?: boolean;
  onClick: () => void;
}

export interface SelectOption {
  value: string;
  label: string;
  sub?: string;
  icon?: React.ReactNode;
  actions?: SelectOptionAction[];
}

interface Props {
  options: SelectOption[];
  value?: string;
  onChange: (value: string) => void;
  onOpenChange?: (open: boolean) => void;
  placeholder?: string;
  searchable?: boolean;
  className?: string;
  dropdownClassName?: string;
}

export default function Select({ options, value, onChange, onOpenChange, placeholder, searchable = false, className = '', dropdownClassName = '' }: Props) {
  const { t } = useTranslation('ui');
  const resolvedPlaceholder = placeholder ?? t('selectPlaceholder');
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [actionMenu, setActionMenu] = useState<{ value: string; top: number; left: number } | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const actionMenuRef = useRef<HTMLDivElement>(null);

  const setDropdownOpen = useCallback((next: boolean) => {
    setOpen(next);
    onOpenChange?.(next);
  }, [onOpenChange]);

  const closeDropdown = useCallback(() => {
    setDropdownOpen(false);
    setSearch('');
    setActionMenu(null);
  }, [setDropdownOpen]);

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      const target = e.target as Node;
      if (ref.current?.contains(target) || actionMenuRef.current?.contains(target)) return;
      closeDropdown();
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [closeDropdown]);

  useEffect(() => {
    if (!open || !searchable) return;
    setTimeout(() => inputRef.current?.focus(), 0);
  }, [open, searchable]);

  useEffect(() => {
    if (!actionMenu) return;
    const closeActionMenu = () => setActionMenu(null);
    window.addEventListener('scroll', closeActionMenu, true);
    window.addEventListener('resize', closeActionMenu);
    return () => {
      window.removeEventListener('scroll', closeActionMenu, true);
      window.removeEventListener('resize', closeActionMenu);
    };
  }, [actionMenu]);

  const filtered = search ? options.filter(o => o.label.toLowerCase().includes(search.toLowerCase()) || o.sub?.toLowerCase().includes(search.toLowerCase())) : options;
  const selected = options.find(o => o.value === value);
  const actionMenuOption = actionMenu ? options.find(o => o.value === actionMenu.value) : null;

  return (
    <div ref={ref} className={`relative ${className}`}>
      <button
        onClick={() => {
          if (open) {
            closeDropdown();
            return;
          }
          setDropdownOpen(true);
          setSearch('');
          setActionMenu(null);
        }}
        className="w-full flex items-center gap-2 text-sm bg-[var(--vsc-bg)] border border-[var(--vsc-border)] rounded px-2 py-1.5 text-left hover:border-zinc-500 transition-colors cursor-pointer"
      >
        <span className={`flex-1 truncate ${selected ? 'text-zinc-300' : 'text-zinc-500'}`}>
          {selected ? selected.label : resolvedPlaceholder}
        </span>
        <ChevronDown className={`w-3 h-3 text-zinc-500 transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>

      {open && (
        <div data-id="select-dropdown" className={`absolute z-50 top-full left-0 right-0 mt-1 min-w-full bg-[#1e1e1e] border border-[var(--vsc-border)] rounded-md shadow-xl overflow-hidden ${dropdownClassName}`}>
          {searchable && (
            <div className="flex items-center gap-1.5 px-2 py-1.5 border-b border-[var(--vsc-border)]">
              <Search className="w-3 h-3 text-zinc-500" />
              <input
                ref={inputRef}
                value={search}
                onChange={e => setSearch(e.target.value)}
                className="flex-1 text-sm bg-transparent text-zinc-300 outline-none placeholder-zinc-600"
                placeholder={t('selectSearchPlaceholder')}
              />
            </div>
          )}
          <div className="max-h-48 overflow-y-auto">
            {filtered.length ? filtered.map(o => {
              const actionMenuOpen = actionMenu?.value === o.value;
              return (
                <div
                  key={o.value}
                  className={`group flex items-center gap-2.5 px-2.5 py-1.5 text-sm transition-colors ${o.value === value ? 'bg-blue-500/10' : 'hover:bg-white/[0.05]'}`}
                >
                  <button
                    type="button"
                    onClick={() => { onChange(o.value); closeDropdown(); }}
                    className="flex min-w-0 flex-1 items-center gap-2.5 text-left cursor-pointer"
                  >
                    {o.icon ? <span className="shrink-0">{o.icon}</span> : null}
                    <div className="min-w-0 flex-1">
                      <span className={`block truncate text-[13px] leading-4 ${o.value === value ? 'text-blue-400' : 'text-zinc-200'}`}>{o.label}</span>
                      {o.sub ? (
                        <span className={`mt-0.5 block truncate text-[10px] leading-3.5 ${o.value === value ? 'text-blue-300/70' : 'text-zinc-500'}`}>
                          {o.sub}
                        </span>
                      ) : null}
                    </div>
                  </button>
                  {o.actions?.length ? (
                    <div className="shrink-0" onMouseDown={(event) => event.stopPropagation()} onClick={(event) => event.stopPropagation()}>
                      <button
                        type="button"
                        data-id="select-option-more"
                        onClick={(event) => {
                          if (actionMenuOpen) {
                            setActionMenu(null);
                            return;
                          }
                          const rect = event.currentTarget.getBoundingClientRect();
                          const menuWidth = 150;
                          const menuHeight = o.actions!.length * 36 + 8;
                          setActionMenu({
                            value: o.value,
                            top: Math.max(8, Math.min(rect.bottom + 4, window.innerHeight - menuHeight - 8)),
                            left: Math.max(8, Math.min(rect.right - menuWidth, window.innerWidth - menuWidth - 8)),
                          });
                        }}
                        className={`flex h-7 w-7 items-center justify-center rounded-md transition-colors ${actionMenuOpen ? 'bg-white/[0.08] text-zinc-200' : 'text-zinc-700 opacity-0 group-hover:opacity-100 hover:bg-white/[0.06] hover:text-zinc-300'}`}
                        title={t('selectMore')}
                      >
                        <MoreHorizontal className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  ) : null}
                </div>
              );
            }) : (
              <div className="px-2 py-3 text-sm text-zinc-600 text-center">{t('selectNoResults')}</div>
            )}
          </div>
        </div>
      )}

      {open && actionMenu && actionMenuOption?.actions?.length ? createPortal(
        <div
          ref={actionMenuRef}
          data-id="select-option-more-dropdown"
          className="fixed z-[260] min-w-[150px] overflow-hidden rounded-lg border border-white/[0.08] bg-[#111113]/98 p-1 shadow-2xl backdrop-blur-xl"
          style={{ top: actionMenu.top, left: actionMenu.left }}
        >
          {actionMenuOption.actions.map((action) => (
            <button
              key={action.id}
              type="button"
              data-id={`select-option-action-${action.id}`}
              onClick={() => {
                action.onClick();
                closeDropdown();
              }}
              className={`flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-sm transition-colors cursor-pointer ${action.danger ? 'text-red-300 hover:bg-red-500/10 hover:text-red-200' : 'text-zinc-300 hover:bg-white/[0.06]'}`}
            >
              {action.icon ? <span className="shrink-0">{action.icon}</span> : null}
              <span>{action.label}</span>
            </button>
          ))}
        </div>,
        document.body
      ) : null}
    </div>
  );
}
