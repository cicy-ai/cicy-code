/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        vsc: {
          bg: 'var(--vsc-bg)',
          'bg-secondary': 'var(--vsc-bg-secondary)',
          'bg-hover': 'var(--vsc-bg-hover)',
          'bg-active': 'var(--vsc-bg-active)',
          'bg-titlebar': 'var(--vsc-bg-titlebar)',
          input: 'var(--vsc-input)',
          border: 'var(--vsc-border)',
          'border-subtle': 'var(--vsc-border-subtle)',
          text: 'var(--vsc-text)',
          'text-secondary': 'var(--vsc-text-secondary)',
          'text-muted': 'var(--vsc-text-muted)',
          'text-disabled': 'var(--vsc-text-disabled)',
          accent: 'var(--vsc-accent)',
          'accent-hover': 'var(--vsc-accent-hover)',
          button: 'var(--vsc-button)',
          'button-hover': 'var(--vsc-button-hover)',
          link: 'var(--vsc-link)',
          success: 'var(--vsc-success)',
          warning: 'var(--vsc-warning)',
          error: 'var(--vsc-error)',
          selection: 'var(--vsc-selection)',
        },
      },
    },
  },
  plugins: [],
}
