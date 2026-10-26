// «USB-носители»: keeps the summary in step with the chosen image and loader.
// The page works without this script; the server renders the same summary.
(function () {
  'use strict';
  var form = document.getElementById('usb');
  if (!form) return;
  function text(id, s) { document.getElementById(id).textContent = s; }
  function update() {
    var image = form.querySelector('input[name=profile_id]:checked');
    var loader = form.querySelector('input[name=loader]:checked');
    if (!image || !loader) return;
    text('usb-image', image.dataset.title);
    text('usb-loader', loader.dataset.title);
    text('usb-archive', 'pxe-uefica-' + image.value + '-' + loader.value + '.zip');
    var size = image.dataset[loader.value];
    text('usb-size', size ? size + ' · ' + form.dataset.cached : form.dataset.build);
  }
  form.addEventListener('change', update);
  update();
})();
