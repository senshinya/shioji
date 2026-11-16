// Bundled at build time. No browser, module loader or network bridge at runtime.
import { render } from 'pele';
globalThis.shiojiDiagram = function (requestJSON) {
  const request = JSON.parse(requestJSON);
  return render(request.Source, {
    fontFamily: 'Arial, Hiragino Sans GB',
    fontFamilyMono: 'Menlo, Hiragino Sans GB',
    responsive: false,
    autoDirection: false,
    limit: 16384,
    maxEdges: 256,
    outputLimit: 2097152,
    links: false,
    images: false,
    measurer: { width: (text, size, style) => nativeMeasure(text, size, style) },
  }).svg;
};
