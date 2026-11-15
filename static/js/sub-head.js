(function(){ try { ['lang','theme-v2'].forEach(function(k){ if (localStorage.getItem('parham-'+k) === null) { var ov = localStorage.getItem('white-'+k); if (ov !== null) localStorage.setItem('parham-'+k, ov); } }); } catch(e) {} })();
/* تم قبل از اولین رندر اعمال می‌شود: پیش‌فرض روشن، بدون فلش شب→روز هنگام رفرش */
(function(){
  try{
    if (localStorage.getItem("parham-theme-v2") === "dark"){
      document.documentElement.setAttribute("data-theme","dark");
      var m = document.querySelector('meta[name="theme-color"]');
      if (m) m.setAttribute("content","#01091a");
    }
  }catch(e){}
})();
