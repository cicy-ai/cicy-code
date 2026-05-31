import { memo, useState, useCallback, createElement, type ComponentProps } from 'react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import 'highlight.js/styles/github-dark.css';
import { Check, Copy } from 'lucide-react';

interface Props {
  source: string;
  className?: string;
}

// react-markdown renders the WHOLE document to a (non-virtualized) DOM tree and
// runs rehype-highlight on every code block — synchronously, on the main thread.
// Past a few hundred KB that locks the UI hard. Markdown files also open in
// preview by default, so a large .md froze the page on open. Above this soft cap
// we show an opt-in instead of auto-rendering.
const MARKDOWN_PREVIEW_SOFT_MAX = 256 * 1024; // chars (~bytes for ASCII)

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

/**
 * Polished markdown renderer for the CodeEditor preview pane.
 *
 * - GFM (tables, task lists, strikethrough, autolinks) via remark-gfm
 * - Syntax-highlighted code blocks via rehype-highlight + highlight.js github-dark
 * - Inline `code` styled distinct from block code
 * - Tables with borders + zebra stripes
 * - External links open in a new tab (rel=noopener)
 * - Images responsive, lazy-loaded, with a subtle border
 * - Copy-to-clipboard button on every fenced code block
 * - Heading anchors via auto-generated id (slugified text), with a hover-only "#"
 */
// Memoized: only re-render when the markdown source (or className) actually
// changes. react-markdown re-parses + re-highlights the whole document on every
// render, so without this any parent re-render (cache revalidation, cursor move,
// save-state change) would rebuild the preview DOM.
function MarkdownPreview({ source, className = '' }: Props) {
  const [forceRender, setForceRender] = useState(false);

  // Guard: don't auto-render an oversized document (would freeze the UI).
  if (source.length > MARKDOWN_PREVIEW_SOFT_MAX && !forceRender) {
    return (
      <div
        data-id="code-editor-markdown-preview-too-large"
        className={`h-full overflow-auto flex items-center justify-center px-8 py-6 ${className}`}
      >
        <div className="max-w-md text-center">
          <div className="text-sm text-zinc-200 mb-1">
            大文件 Markdown（{formatBytes(source.length)}）
          </div>
          <div className="text-xs text-zinc-500 mb-4 leading-5">
            渲染整篇预览会卡住界面。建议切换到「源码」查看；如确需预览,可强制渲染（可能短暂卡顿）。
          </div>
          <button
            type="button"
            onClick={() => setForceRender(true)}
            className="px-3 py-1.5 rounded-md border border-amber-700/50 bg-amber-900/20 text-amber-200 text-xs hover:bg-amber-900/40 transition-colors"
          >
            仍要渲染预览
          </button>
        </div>
      </div>
    );
  }

  return (
    <div
      data-id="code-editor-markdown-preview"
      className={`h-full overflow-auto px-8 py-6 text-zinc-200 leading-relaxed ${className}`}
    >
      <div className="max-w-[78ch] mx-auto markdown-body">
        <Markdown
          remarkPlugins={[remarkGfm]}
          rehypePlugins={[[rehypeHighlight, { detect: true, ignoreMissing: true }]]}
          components={{
            h1: ({ children, ...rest }) => (
              <Heading level={1} {...rest}>{children}</Heading>
            ),
            h2: ({ children, ...rest }) => (
              <Heading level={2} {...rest}>{children}</Heading>
            ),
            h3: ({ children, ...rest }) => (
              <Heading level={3} {...rest}>{children}</Heading>
            ),
            h4: ({ children, ...rest }) => (
              <Heading level={4} {...rest}>{children}</Heading>
            ),
            h5: ({ children, ...rest }) => (
              <Heading level={5} {...rest}>{children}</Heading>
            ),
            h6: ({ children, ...rest }) => (
              <Heading level={6} {...rest}>{children}</Heading>
            ),
            p: ({ children }) => (
              <p className="my-3 text-[15px] text-zinc-200 leading-7">{children}</p>
            ),
            a: ({ href, children, ...rest }) => {
              const external = !!href && /^https?:\/\//i.test(href);
              return (
                <a
                  {...rest}
                  href={href}
                  target={external ? '_blank' : undefined}
                  rel={external ? 'noopener noreferrer' : undefined}
                  className="text-sky-400 hover:text-sky-300 underline decoration-sky-400/40 hover:decoration-sky-300/80 underline-offset-2"
                >
                  {children}
                </a>
              );
            },
            ul: ({ children, className: cls }) => (
              <ul
                className={`my-3 ml-6 list-disc text-[15px] marker:text-zinc-500 space-y-1 ${
                  cls?.includes('contains-task-list') ? 'list-none ml-0' : ''
                }`}
              >
                {children}
              </ul>
            ),
            ol: ({ children }) => (
              <ol className="my-3 ml-6 list-decimal text-[15px] marker:text-zinc-500 space-y-1">
                {children}
              </ol>
            ),
            li: ({ children, className: cls, ...rest }) => (
              <li
                className={`text-zinc-200 leading-7 ${
                  cls?.includes('task-list-item') ? 'flex items-baseline gap-2 list-none -ml-1' : ''
                }`}
                {...rest}
              >
                {children}
              </li>
            ),
            blockquote: ({ children }) => (
              <blockquote className="my-3 border-l-2 border-sky-700/60 bg-sky-900/10 pl-4 pr-3 py-1 text-zinc-300 italic">
                {children}
              </blockquote>
            ),
            hr: () => <hr className="my-6 border-zinc-800" />,
            table: ({ children }) => (
              <div className="my-4 overflow-x-auto rounded border border-zinc-800">
                <table className="w-full border-collapse text-sm">{children}</table>
              </div>
            ),
            thead: ({ children }) => <thead className="bg-zinc-900">{children}</thead>,
            tr: ({ children }) => <tr className="border-b border-zinc-800 last:border-b-0 even:bg-zinc-900/40">{children}</tr>,
            th: ({ children, align }) => (
              <th
                className="px-3 py-2 font-semibold text-zinc-100 border-r border-zinc-800 last:border-r-0"
                style={align ? { textAlign: align as React.CSSProperties['textAlign'] } : undefined}
              >
                {children}
              </th>
            ),
            td: ({ children, align }) => (
              <td
                className="px-3 py-2 text-zinc-200 border-r border-zinc-800 last:border-r-0"
                style={align ? { textAlign: align as React.CSSProperties['textAlign'] } : undefined}
              >
                {children}
              </td>
            ),
            img: ({ src, alt, ...rest }) => (
              <img
                src={src}
                alt={alt || ''}
                loading="lazy"
                className="my-3 max-w-full rounded border border-zinc-800"
                {...rest}
              />
            ),
            code: ({ className: cls, children, ...rest }) => {
              // react-markdown passes className like "language-go hljs"; the
              // hljs class only appears on block code. Distinguish so inline
              // and block get different styling.
              const isBlock = (cls || '').includes('hljs') || /\bcontains-task-list\b/.test(cls || '');
              if (isBlock) {
                return (
                  <code className={cls} {...rest}>
                    {children}
                  </code>
                );
              }
              return (
                <code
                  className="px-1.5 py-0.5 rounded bg-zinc-800/70 text-amber-200 font-mono text-[0.9em] border border-zinc-700/40"
                  {...rest}
                >
                  {children}
                </code>
              );
            },
            pre: (props) => <PreWithCopy {...props} />,
            input: ({ type, checked, disabled, ...rest }) => {
              // GFM task list checkboxes — make them visible against the dark bg.
              if (type === 'checkbox') {
                return (
                  <input
                    type="checkbox"
                    checked={!!checked}
                    disabled={disabled}
                    readOnly
                    className="mr-1 align-middle accent-sky-500"
                    {...rest}
                  />
                );
              }
              return <input type={type} {...rest} />;
            },
          }}
        >
          {source}
        </Markdown>
      </div>
    </div>
  );
}

export default memo(MarkdownPreview);

// --- helpers ---------------------------------------------------------------

function slugify(text: string): string {
  return text
    .toLowerCase()
    .trim()
    .replace(/[^\w\s一-龥-]/g, '')
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-');
}

function textOf(node: React.ReactNode): string {
  if (typeof node === 'string') return node;
  if (typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(textOf).join('');
  if (node && typeof node === 'object' && 'props' in node) {
    return textOf((node as any).props?.children);
  }
  return '';
}

function Heading({
  level,
  children,
}: {
  level: 1 | 2 | 3 | 4 | 5 | 6;
  children: React.ReactNode;
}) {
  const id = slugify(textOf(children));
  const sizes = {
    1: 'text-3xl mt-6 mb-3 pb-2 border-b border-zinc-800 font-bold',
    2: 'text-2xl mt-6 mb-2 pb-1 border-b border-zinc-800/70 font-semibold',
    3: 'text-xl  mt-5 mb-2 font-semibold',
    4: 'text-lg  mt-4 mb-2 font-semibold',
    5: 'text-base mt-4 mb-1 font-semibold',
    6: 'text-sm mt-3 mb-1 font-semibold text-zinc-300',
  }[level];
  return createElement(
    `h${level}`,
    { id, className: `${sizes} text-zinc-100 group scroll-mt-4` },
    <a
      href={`#${id}`}
      className="opacity-0 group-hover:opacity-50 hover:!opacity-100 mr-2 text-sky-400 no-underline"
      aria-label="permalink"
    >
      #
    </a>,
    children,
  );
}

function PreWithCopy(props: ComponentProps<'pre'>) {
  const { children, className: cls, ...rest } = props;
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(() => {
    // Walk the children for raw text — react-markdown wraps code in <code>.
    const text = textOf(children as React.ReactNode);
    if (!text) return;
    navigator.clipboard?.writeText(text).then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    });
  }, [children]);

  return (
    <div className="relative my-4 group/pre">
      <pre
        className={`overflow-x-auto rounded-lg border border-zinc-800 bg-[#0d1117] p-4 text-[13px] leading-6 ${cls || ''}`}
        {...rest}
      >
        {children}
      </pre>
      <button
        type="button"
        onClick={handleCopy}
        className="absolute top-2 right-2 opacity-0 group-hover/pre:opacity-100 transition-opacity text-xs px-2 py-1 rounded bg-zinc-800/80 hover:bg-zinc-700 text-zinc-300 inline-flex items-center gap-1"
        title={copied ? '已复制' : '复制'}
      >
        {copied ? <Check className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
        {copied ? '已复制' : '复制'}
      </button>
    </div>
  );
}
