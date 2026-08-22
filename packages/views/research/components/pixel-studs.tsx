/**
 * Four corner studs for a `.px-frame` panel (research home pixel theme).
 * Rendered as spans because the stud offsets are fixed-pixel and the frame
 * width is fluid — pseudo-elements can only draw two corners per element.
 */
export function PixelStuds() {
  return (
    <>
      <span className="px-stud" aria-hidden />
      <span className="px-stud" aria-hidden />
      <span className="px-stud" aria-hidden />
      <span className="px-stud" aria-hidden />
    </>
  );
}
