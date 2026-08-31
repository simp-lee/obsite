const SVG_NS = "http://www.w3.org/2000/svg";
let runtimeOptions = { startOnLoad: true };

function collectBlocks() {
  if (typeof document === "undefined" || !document.querySelectorAll) {
    return [];
  }

  return Array.prototype.slice.call(document.querySelectorAll("pre.mermaid"));
}

function normalizeSource(source) {
  return String(source || "").replace(/\r\n?/g, "\n").trim();
}

function sanitizeLabel(label) {
  var value = String(label || "").trim();
  if (!value) {
    return "";
  }

  value = value.replace(/^graph\s+[A-Z]{2}\s*/i, "");
  value = value.replace(/^[A-Za-z0-9_]+\[(.+)\]$/, "$1");
  value = value.replace(/^['\"\[{(]+|['\"\]})]+$/g, "");
  value = value.replace(/<[^>]+>/g, " ");
  value = value.replace(/\|/g, " ");
  return value.replace(/\s+/g, " ").trim();
}

function parseDiagram(source) {
  var normalized = normalizeSource(source);
  var direction = "TD";
  var lines = normalized.replace(/;/g, "\n").split("\n").map(function (line) {
    return line.trim();
  }).filter(Boolean);

  if (lines.length > 0) {
    var graphMatch = /^graph\s+([A-Z]{2})\b/i.exec(lines[0]);
    if (graphMatch) {
      direction = graphMatch[1].toUpperCase();
      lines = lines.slice(1);
    }
  }

  var nodes = [];
  var seen = Object.create(null);
  var edges = [];

  function addNode(label) {
    var cleaned = sanitizeLabel(label);
    if (!cleaned) {
      return "";
    }
    if (!seen[cleaned]) {
      seen[cleaned] = true;
      nodes.push(cleaned);
    }
    return cleaned;
  }

  for (var i = 0; i < lines.length; i++) {
    var edgeMatch = /(.+?)-+.*?>\s*(.+)/.exec(lines[i]);
    if (!edgeMatch) {
      addNode(lines[i]);
      continue;
    }

    var from = addNode(edgeMatch[1]);
    var to = addNode(edgeMatch[2]);
    if (from && to) {
      edges.push({ from: from, to: to });
    }
  }

  if (nodes.length === 0) {
    nodes = ["Mermaid", "Diagram"];
    edges = [{ from: "Mermaid", to: "Diagram" }];
  } else if (edges.length === 0 && nodes.length > 1) {
    for (var index = 0; index < nodes.length - 1; index++) {
      edges.push({ from: nodes[index], to: nodes[index + 1] });
    }
  }

  return { direction: direction, nodes: nodes, edges: edges };
}

function createSVGNode(name) {
  return document.createElementNS(SVG_NS, name);
}

function setSVGAttrs(node, attrs) {
  Object.keys(attrs).forEach(function (key) {
    node.setAttribute(key, String(attrs[key]));
  });
  return node;
}

function buildSVG(diagram) {
  var horizontal = diagram.direction === "LR" || diagram.direction === "RL";
  var orderedNodes = diagram.nodes.slice();
  if (diagram.direction === "RL" || diagram.direction === "BT") {
    orderedNodes.reverse();
  }

  var boxWidth = 166;
  var boxHeight = 68;
  var gap = 54;
  var padding = 28;
  var width = horizontal ? padding * 2 + orderedNodes.length * boxWidth + Math.max(0, orderedNodes.length - 1) * gap : padding * 2 + boxWidth;
  var height = horizontal ? padding * 2 + boxHeight : padding * 2 + orderedNodes.length * boxHeight + Math.max(0, orderedNodes.length - 1) * gap;
  var positions = Object.create(null);

  orderedNodes.forEach(function (label, index) {
    positions[label] = horizontal ? {
      x: padding + index * (boxWidth + gap),
      y: padding
    } : {
      x: padding,
      y: padding + index * (boxHeight + gap)
    };
  });

  var svg = setSVGAttrs(createSVGNode("svg"), {
    viewBox: "0 0 " + width + " " + height,
    role: "img",
    "aria-label": "Rendered Mermaid diagram"
  });

  var defs = createSVGNode("defs");
  var marker = setSVGAttrs(createSVGNode("marker"), {
    id: "obsite-mermaid-arrow",
    markerWidth: 10,
    markerHeight: 10,
    refX: 8,
    refY: 5,
    orient: "auto",
    markerUnits: "strokeWidth"
  });
  marker.appendChild(setSVGAttrs(createSVGNode("path"), {
    d: "M0 0 L10 5 L0 10 z",
    class: "mermaid-arrow"
  }));
  defs.appendChild(marker);
  svg.appendChild(defs);

  diagram.edges.forEach(function (edge) {
    var from = positions[edge.from];
    var to = positions[edge.to];
    if (!from || !to) {
      return;
    }

    var line = setSVGAttrs(createSVGNode("line"), horizontal ? {
      x1: from.x + boxWidth,
      y1: from.y + boxHeight / 2,
      x2: to.x,
      y2: to.y + boxHeight / 2,
      class: "mermaid-edge",
      "marker-end": "url(#obsite-mermaid-arrow)"
    } : {
      x1: from.x + boxWidth / 2,
      y1: from.y + boxHeight,
      x2: to.x + boxWidth / 2,
      y2: to.y,
      class: "mermaid-edge",
      "marker-end": "url(#obsite-mermaid-arrow)"
    });
    svg.appendChild(line);
  });

  orderedNodes.forEach(function (label) {
    var pos = positions[label];
    var rect = setSVGAttrs(createSVGNode("rect"), {
      x: pos.x,
      y: pos.y,
      rx: 18,
      ry: 18,
      width: boxWidth,
      height: boxHeight,
      class: "mermaid-node"
    });
    var text = setSVGAttrs(createSVGNode("text"), {
      x: pos.x + boxWidth / 2,
      y: pos.y + boxHeight / 2,
      class: "mermaid-label"
    });
    text.textContent = label;
    svg.appendChild(rect);
    svg.appendChild(text);
  });

  return svg;
}

function renderBlock(block) {
  if (!block || block.getAttribute("data-obsite-mermaid-ready") === "true") {
    return;
  }

  var diagram = parseDiagram(block.textContent || "");
  block.setAttribute("data-obsite-mermaid-ready", "true");
  block.classList.add("mermaid-rendered");
  block.setAttribute("role", "img");
  block.setAttribute("aria-label", "Mermaid diagram");
  block.textContent = "";
  block.appendChild(buildSVG(diagram));
}

function runWhenReady(callback) {
  if (typeof document === "undefined") {
    return;
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", callback, { once: true });
    return;
  }
  callback();
}

const mermaid = {
  initialize(options = {}) {
    runtimeOptions = Object.assign({}, runtimeOptions, options);
    if (runtimeOptions.startOnLoad === false) {
      return;
    }

    runWhenReady(function () {
      mermaid.run();
    });
  },

  run() {
    collectBlocks().forEach(renderBlock);
  }
};

export default mermaid;