/* Country metadata and formatting: libphonenumber-js 1.13.16 (MIT), vendored alongside this file. */
(function(global){
 'use strict';
 var lib=global.libphonenumber, mounted=new WeakSet();
 var words={en:['Country calling code','Search country or calling code','Enter a valid phone number'],fr:['Indicatif du pays','Rechercher un pays ou un indicatif','Saisissez un numéro de téléphone valide']};
 Object.assign(words,{es:['Indicativo del país','Buscar país o indicativo','Introduce un teléfono válido'],de:['Ländervorwahl','Land oder Vorwahl suchen','Gültige Telefonnummer eingeben'],it:['Prefisso internazionale','Cerca paese o prefisso','Inserisci un numero valido'],nl:['Landcode','Zoek land of landcode','Voer een geldig telefoonnummer in'],pt:['Indicativo do país','Pesquisar país ou indicativo','Introduza um telefone válido'],pl:['Numer kierunkowy kraju','Szukaj kraju lub numeru kierunkowego','Wpisz prawidłowy numer telefonu'],sv:['Landsnummer','Sök land eller landsnummer','Ange ett giltigt telefonnummer'],el:['Κωδικός χώρας','Αναζήτηση χώρας ή κωδικού','Εισαγάγετε έγκυρο αριθμό τηλεφώνου']});
 function mount(root,language){
  if(!lib)return;
  var lang=language||document.documentElement.lang||navigator.language||'en', text=words[lang.split('-')[0]]||words.en;
  var names=new Intl.DisplayNames([lang],{type:'region'});
  var countries=lib.getCountries().map(function(code){return{code:code,name:names.of(code),dial:lib.getCountryCallingCode(code)};}).sort(function(a,b){return a.name.localeCompare(b.name,lang);});
  root.querySelectorAll('input[type="tel"]').forEach(function(input){
   if(mounted.has(input))return;mounted.add(input);
   var country;try{country=new Intl.Locale(navigator.language).region;}catch(e){}if(!lib.getCountries().includes(country))country='US';
   var wrapper=document.createElement('div');wrapper.className='phone-control';input.before(wrapper);
   var button=document.createElement('button');button.type='button';button.className='phone-country';button.setAttribute('aria-label',text[0]);button.setAttribute('aria-expanded','false');wrapper.append(button,input);
   var panel=document.createElement('div');panel.className='phone-country-panel';panel.hidden=true;if(panel.showPopover)panel.popover='manual';
   var search=document.createElement('input');search.type='search';search.placeholder=text[1];search.setAttribute('aria-label',text[1]);
   var list=document.createElement('div');list.className='phone-country-options';panel.append(search,list);wrapper.append(panel);
   function flag(c){return String.fromCodePoint.apply(null,c.split('').map(function(x){return 127397+x.charCodeAt(0);}));}
   function update(){button.textContent=flag(country)+' +'+lib.getCountryCallingCode(country)+' ▾';button.title=names.of(country);button.setAttribute('aria-label',text[0]+': '+names.of(country)+' +'+lib.getCountryCallingCode(country));input.placeholder='+'+lib.getCountryCallingCode(country)+' …';}
   function close(){if(panel.hidePopover&&panel.matches(':popover-open'))panel.hidePopover();panel.hidden=true;button.setAttribute('aria-expanded','false');}
   function render(){list.replaceChildren();var q=search.value.trim().toLocaleLowerCase();countries.filter(function(c){return(c.name+' '+c.code+' +'+c.dial).toLocaleLowerCase().includes(q);}).forEach(function(c){var item=document.createElement('button');item.type='button';item.textContent=flag(c.code)+' '+c.name+' +'+c.dial;item.setAttribute('aria-pressed',String(c.code===country));item.onclick=function(){country=c.code;update();close();input.setCustomValidity('');input.focus();};list.append(item);});}
   button.onclick=function(){panel.hidden=!panel.hidden;button.setAttribute('aria-expanded',String(!panel.hidden));if(!panel.hidden){search.value='';render();if(panel.showPopover){var r=button.getBoundingClientRect();panel.style.position='fixed';panel.style.left=Math.max(8,Math.min(r.left,innerWidth-360))+'px';panel.style.top=Math.min(r.bottom+6,Math.max(8,innerHeight-310))+'px';panel.style.width=Math.min(360,innerWidth-16)+'px';panel.showPopover();}search.focus();}};
   search.oninput=render;wrapper.addEventListener('keydown',function(e){if(e.key==='Escape'){close();button.focus();}if(e.key==='ArrowDown'&&e.target===search){e.preventDefault();list.querySelector('button')?.focus();}});
   wrapper.addEventListener('focusout',function(e){if(!wrapper.contains(e.relatedTarget))close();});
   input.addEventListener('input',function(){input.setCustomValidity('');});
   input.addEventListener('blur',function(){var parsed=lib.parsePhoneNumberFromString(input.value,country);if(parsed&&parsed.isValid()){country=parsed.country||country;input.value=parsed.formatInternational();update();}});
   if(input.form)input.form.addEventListener('submit',function(e){if(!input.value.trim()){input.setCustomValidity('');return;}var parsed=lib.parsePhoneNumberFromString(input.value,country);if(!parsed||!parsed.isValid()){e.preventDefault();e.stopImmediatePropagation();input.setCustomValidity(text[2]);input.reportValidity();return;}input.value=parsed.number;},true);
   update();
  });
 }
 global.CalnodePhoneInput={mount:mount};

})(globalThis);
