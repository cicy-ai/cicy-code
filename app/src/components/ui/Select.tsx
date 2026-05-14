import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ChevronDown, MoreHorizontal, Search, X } from 'lucide-react';
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
  triggerIcon?: React.ReactNode;
  footer?: React.ReactNode;
  /** CSS selector for an ancestor whose width the dropdown should match. When set, the dropdown is portaled and positioned to span the matched element horizontally. */
  dropdownMatchSelector?: string;
}

export default function Select({
  options,
  value,
  onChange,
  onOpenChange,
  placeholder,
  searchable = false,
  className = '',
  dropdownClassName = '',
  triggerIcon,
  footer,
  dropdownMatchSelector,
}: Props) {
  const { t } = useTranslation('ui');
  const resolvedPlaceholder = placeholder ?? t('selectPlaceholder');
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [activeIndex, setActiveIndex] = useState(0);
  const [actionMenu, setActionMenu] = useState<{ value: string; top: number; left: number } | null>(null);
  const [portalRect, setPortalRect] = useState<{ top: number; left: number; width: number } | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const actionMenuRef = useRef<HTMLDivElement>(null);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const setDropdownOpen = useCallback((next: boolean) => {
    setOpen(next);
    onOpenChange?.(next);
  }, [onOpenChange]);

  const closeDropdown = useCallback(() => {
    setDropdownOpen(false);
    setSearch('');
    setActionMenu(null);
    setActiveIndex(0);
  }, [setDropdownOpen]);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      const target = e.target as Node;
      if (ref.current?.contains(target) || actionMenuRef.current?.contains(target) || dropdownRef.current?.contains(target)) return;
      closeDropdown();
    };
    const handleBlur = () => closeDropdown();
    document.addEventListener('mousedown', handler);
    window.addEventListener('blur', handleBlur);
    return () => {
      document.removeEventListener('mousedown', handler);
      window.removeEventListener('blur', handleBlur);
    };
  }, [open, closeDropdown]);

  useEffect(() => {
    if (!open || !dropdownMatchSelector) {
      setPortalRect(null);
      return;
    }
    const compute = () => {
      const matchEl = ref.current?.closest(dropdownMatchSelector) as HTMLElement | null
        ?? (document.querySelector(dropdownMatchSelector) as HTMLElement | null);
      const triggerEl = ref.current;
      if (!matchEl || !triggerEl) return;
      const m = matchEl.getBoundingClientRect();
      const tr = triggerEl.getBoundingClientRect();
      setPortalRect({ top: tr.bottom + 6, left: m.left, width: m.width });
    };
    compute();
    window.addEventListener('resize', compute);
    window.addEventListener('scroll', compute, true);
    return () => {
      window.removeEventListener('resize', compute);
      window.removeEventListener('scroll', compute, true);
    };
  }, [open, dropdownMatchSelector]);

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

  const filtered = useMemo(() => {
    if (!search) return options;
    const q = search.toLowerCase();
    return options.filter(o => o.label.toLowerCase().includes(q) || o.sub?.toLowerCase().includes(q));
  }, [options, search]);

  useEffect(() => {
    setActiveIndex(0);
  }, [search, open]);

  useEffect(() => {
    if (!open) return;
    const el = listRef.current?.querySelector('[data-active-row="true"]') as HTMLElement | null;
    el?.scrollIntoView({ block: 'nearest' });
  }, [activeIndex, open]);

  const selected = options.find(o => o.value === value);
  const actionMenuOption = actionMenu ? options.find(o => o.value === actionMenu.value) : null;

  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (!open) {
      if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
        e.preventDefault();
        setDropdownOpen(true);
      }
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActiveIndex(i => (filtered.length ? (i + 1) % filtered.length : 0));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActiveIndex(i => (filtered.length ? (i - 1 + filtered.length) % filtered.length : 0));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const opt = filtered[activeIndex];
      if (opt) {
        onChange(opt.value);
        closeDropdown();
      }
    } else if (e.key === 'Escape') {
      e.preventDefault();
      closeDropdown();
    }
  }, [open, filtered, activeIndex, onChange, closeDropdown, setDropdownOpen]);

  return (
    <div data-id="select-auto-1" ref={ref} className={`relative ${className}`} onKeyDown={handleKeyDown}>
      <button data-id="select-auto-2"
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => {
          if (open) { closeDropdown(); return; }
          setDropdownOpen(true);
          setSearch('');
          setActionMenu(null);
        }}
        className={`group/trigger w-full flex items-center gap-2 h-9 text-[13px] rounded-lg px-3 text-left transition-colors duration-150 cursor-pointer
          ${open
            ? 'bg-white/[0.045] border border-blue-500/55 ring-1 ring-blue-500/15'
            : 'bg-white/[0.025] border border-white/[0.09] hover:bg-white/[0.04] hover:border-white/[0.14]'}
          focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/25`}
      >
        {triggerIcon ? (
          <span data-id="select-auto-3" className={`shrink-0 transition-colors ${open ? 'text-blue-300' : 'text-zinc-500 group-hover/trigger:text-zinc-300'}`}>
            {triggerIcon}
          </span>
        ) : null}
        <span data-id="select-auto-4" className={`flex-1 truncate ${selected ? 'text-zinc-200' : 'text-zinc-500'}`}>
          {selected ? selected.label : resolvedPlaceholder}
        </span>
        <ChevronDown className={`w-3.5 h-3.5 transition-all duration-200 ${open ? 'rotate-180 text-zinc-300' : 'text-zinc-500 group-hover/trigger:text-zinc-300'}`} />
      </button>

      {open && (() => {
        const usePortal = !!dropdownMatchSelector;
        if (usePortal && !portalRect) return null;
        const dropdownNode = (
        <div
          ref={dropdownRef}
          data-id="select-dropdown"
          role="listbox"
          className={`${usePortal ? 'fixed z-[200]' : 'absolute z-50 top-full left-0 right-0 mt-1.5 min-w-full'} overflow-hidden
            rounded-xl border border-white/[0.06] bg-[#141416]/[0.98] backdrop-blur-md
            shadow-[0_20px_50px_-12px_rgba(0,0,0,0.7),inset_0_1px_0_0_rgba(255,255,255,0.04)]
            animate-select-in ${dropdownClassName}`}
          style={usePortal && portalRect ? { top: portalRect.top, left: portalRect.left, width: portalRect.width } : undefined}
        >
          {searchable && (
            <div data-id="select-auto-5" className="flex items-center gap-2 px-3 py-2.5 border-b border-white/[0.05]">
              <Search className="w-3.5 h-3.5 text-zinc-500 shrink-0" />
              <input data-id="select-auto-6"
                ref={inputRef}
                value={search}
                onChange={e => setSearch(e.target.value)}
                className="flex-1 text-[13px] bg-transparent text-zinc-200 outline-none placeholder:text-zinc-600"
                placeholder={t('selectSearchPlaceholder')}
              />
              {search ? (
                <button data-id="select-auto-7"
                  type="button"
                  onClick={() => { setSearch(''); inputRef.current?.focus(); }}
                  className="flex h-5 w-5 items-center justify-center rounded text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300"
                  title={t('selectClear')}
                >
                  <X className="w-3 h-3" />
                </button>
              ) : (
                <kbd className="hidden md:inline-flex h-5 items-center rounded border border-white/[0.06] bg-white/[0.02] px-1.5 text-[10px] font-mono text-zinc-500 select-none">
                  Esc
                </kbd>
              )}
            </div>
          )}

          <div data-id="select-auto-8" ref={listRef} className="max-h-80 overflow-y-auto py-1 hide-scrollbar">
            {filtered.length ? filtered.map((o, idx) => {
              const actionMenuOpen = actionMenu?.value === o.value;
              const isSelected = o.value === value;
              const isActive = idx === activeIndex;
              return (
                <div data-id="select-auto-9"
                  key={o.value}
                  data-active-row={isActive}
                  role="option"
                  aria-selected={isSelected}
                  onMouseEnter={() => setActiveIndex(idx)}
                  className={`group/row mx-1 flex items-center gap-2 rounded-lg px-2 py-1.5 transition-colors relative
                    ${isSelected ? 'bg-blue-500/[0.12]' : isActive ? 'bg-white/[0.05]' : ''}`}
                >
                  {isSelected ? (
                    <span data-id="select-auto-10" className="absolute left-0 top-1/2 -translate-y-1/2 h-5 w-[2px] rounded-r-full bg-blue-400" />
                  ) : null}
                  <button data-id="select-auto-11"
                    type="button"
                    onClick={() => { onChange(o.value); closeDropdown(); }}
                    className="flex min-w-0 flex-1 items-center gap-2.5 text-left cursor-pointer"
                  >
                    {o.icon ? <span className="shrink-0">{o.icon}</span> : null}
                    <div data-id="select-auto-12" className="min-w-0 flex-1">
                      <span data-id="select-auto-13" className={`block truncate text-[13px] leading-tight font-medium ${isSelected ? 'text-blue-200' : 'text-zinc-200'}`}>
                        {o.label}
                      </span>
                      {o.sub ? (
                        <span data-id="select-auto-14" className={`mt-0.5 block truncate font-mono text-[11px] leading-tight ${isSelected ? 'text-blue-300/60' : 'text-zinc-500'}`}>
                          {o.sub}
                        </span>
                      ) : null}
                    </div>
                  </button>
                  {o.actions?.length ? (
                    <div data-id="select-auto-15"
                      className="shrink-0"
                      onMouseDown={(event) => event.stopPropagation()}
                      onClick={(event) => event.stopPropagation()}
                    >
                      <button
                        type="button"
                        data-id="select-option-more"
                        onClick={(event) => {
                          if (actionMenuOpen) { setActionMenu(null); return; }
                          const rect = event.currentTarget.getBoundingClientRect();
                          const menuWidth = 170;
                          const menuHeight = o.actions!.length * 36 + 10;
                          setActionMenu({
                            value: o.value,
                            top: Math.max(8, Math.min(rect.bottom + 4, window.innerHeight - menuHeight - 8)),
                            left: Math.max(8, Math.min(rect.right - menuWidth, window.innerWidth - menuWidth - 8)),
                          });
                        }}
                        className={`flex h-6 w-6 items-center justify-center rounded-md transition-all
                          ${actionMenuOpen
                            ? 'bg-white/[0.10] text-zinc-200'
                            : 'text-zinc-500 opacity-0 group-hover/row:opacity-100 hover:bg-white/[0.08] hover:text-zinc-200'}`}
                        title={t('selectMore')}
                      >
                        <MoreHorizontal className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  ) : null}
                </div>
              );
            }) : (
              <div data-id="select-auto-16" className="flex flex-col items-center justify-center gap-1.5 px-4 py-7">
                <Search className="w-5 h-5 text-zinc-700" />
                <p className="text-[13px] text-zinc-500">{t('selectNoResults')}</p>
                {search ? (
                  <button data-id="select-auto-17"
                    type="button"
                    onClick={() => { setSearch(''); inputRef.current?.focus(); }}
                    className="mt-1 text-[11px] text-zinc-500 transition-colors hover:text-zinc-300 cursor-pointer"
                  >
                    {t('selectClear')}
                  </button>
                ) : null}
              </div>
            )}
          </div>

          {footer ? (
            <div data-id="select-auto-18" className="border-t border-white/[0.05] bg-white/[0.01]">
              {footer}
            </div>
          ) : null}

          {searchable && filtered.length > 0 ? (
            <div data-id="select-auto-19" className="hidden md:flex items-center justify-between gap-3 border-t border-white/[0.05] bg-white/[0.01] px-3 py-1.5 text-[10px] text-zinc-600 select-none">
              <div data-id="select-auto-20" className="flex items-center gap-2">
                <span data-id="select-auto-21" className="inline-flex items-center gap-1">
                  <kbd className="inline-flex h-4 min-w-4 items-center justify-center rounded border border-white/[0.06] bg-white/[0.02] px-1 font-mono">↑</kbd>
                  <kbd className="inline-flex h-4 min-w-4 items-center justify-center rounded border border-white/[0.06] bg-white/[0.02] px-1 font-mono">↓</kbd>
                  <span data-id="select-auto-22">{t('selectHintNavigate')}</span>
                </span>
                <span data-id="select-auto-23" className="inline-flex items-center gap-1">
                  <kbd className="inline-flex h-4 items-center justify-center rounded border border-white/[0.06] bg-white/[0.02] px-1 font-mono">↵</kbd>
                  <span data-id="select-auto-24">{t('selectHintSelect')}</span>
                </span>
              </div>
              <span data-id="select-auto-25" className="font-mono">{filtered.length}</span>
            </div>
          ) : null}
        </div>
        );
        return usePortal ? createPortal(dropdownNode, document.body) : dropdownNode;
      })()}

      {open && actionMenu && actionMenuOption?.actions?.length ? createPortal(
        <div
          ref={actionMenuRef}
          data-id="select-option-more-dropdown"
          className="fixed z-[260] min-w-[170px] overflow-hidden rounded-lg border border-white/[0.08] bg-[#111113]/[0.98] p-1 shadow-2xl backdrop-blur-md animate-select-in"
          style={{ top: actionMenu.top, left: actionMenu.left }}
        >
          {actionMenuOption.actions.map((action) => (
            <button
              key={action.id}
              type="button"
              data-id={`select-option-action-${action.id}`}
              onClick={() => { action.onClick(); closeDropdown(); }}
              className={`flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-sm transition-colors cursor-pointer ${
                action.danger
                  ? 'text-red-300 hover:bg-red-500/10 hover:text-red-200'
                  : 'text-zinc-300 hover:bg-white/[0.06]'
              }`}
            >
              {action.icon ? <span className="shrink-0">{action.icon}</span> : null}
              <span data-id="select-auto-26">{action.label}</span>
            </button>
          ))}
        </div>,
        document.body
      ) : null}
    </div>
  );
}
