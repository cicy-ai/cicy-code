import "./Card.css";

/**
 * <Card> base surface. Variants:
 *   plain     — flat, no hover (default)
 *   interactive — hover lift + cursor pointer
 *   accent    — left accent bar (color via prop accentColor)
 */
export default function Card({ children, variant = "plain", accentColor, onClick, className = "", ...rest }) {
  const Tag = onClick ? "button" : "div";
  return (
    <Tag
      className={`card card--${variant} ${className}`}
      onClick={onClick}
      style={accentColor ? { "--accent": accentColor } : undefined}
      {...rest}
    >
      {children}
    </Tag>
  );
}
