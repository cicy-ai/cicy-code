import "./Button.css";

/**
 * <Button variant="primary|ghost|danger" size="sm|md" loading disabled icon={...}>
 *   Label
 * </Button>
 *
 * Sizes:
 *   sm = 28px tall (compact toolbars)
 *   md = 36px tall (default — meets touch target on mobile)
 */
export default function Button({
  children,
  variant = "ghost",
  size = "md",
  loading = false,
  disabled = false,
  icon = null,
  iconAfter = null,
  className = "",
  type = "button",
  ...rest
}) {
  return (
    <button
      type={type}
      className={`btn btn--${variant} btn--${size} ${loading ? "is-loading" : ""} no-drag ${className}`}
      disabled={disabled || loading}
      {...rest}
    >
      {loading && <span className="btn__spinner" aria-hidden="true" />}
      {icon && <span className="btn__icon">{icon}</span>}
      {children && <span className="btn__label">{children}</span>}
      {iconAfter && <span className="btn__icon">{iconAfter}</span>}
    </button>
  );
}
