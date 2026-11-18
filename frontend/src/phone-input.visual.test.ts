import {test,expect,afterEach} from 'vitest';
import library from '../../internal/handler/assets/libphonenumber-min.js?raw';
import controls from '../../internal/handler/assets/phone-input.js?raw';
import '../../internal/handler/templates/booking.css';
(0,eval)(library);(0,eval)(controls);
afterEach(()=>{document.querySelector('#phone-test')?.remove();});
test.each(['light','dark'])('country picker normalizes Indian numbers in %s mode',async(mode)=>{
 const wasDark=document.documentElement.classList.contains('dark');document.documentElement.classList.toggle('dark',mode==='dark');
 const form=document.createElement('form');form.id='phone-test';form.innerHTML='<label for="test-phone">Phone</label><input id="test-phone" type="tel">';document.body.append(form);
 try{
  (globalThis as typeof globalThis & {CalnodePhoneInput:{mount(root:ParentNode,language:string):void}}).CalnodePhoneInput.mount(form,'en');form.querySelector<HTMLButtonElement>('.phone-country')!.click();
  [...form.querySelectorAll<HTMLButtonElement>('.phone-country-options button')].find(b=>b.textContent?.includes('India +91'))!.click();
  const input=form.querySelector<HTMLInputElement>('input[type="tel"]')!;input.value='9876543210';
  expect(form.dispatchEvent(new Event('submit',{cancelable:true,bubbles:true}))).toBe(true);expect(input.value).toBe('+919876543210');
  input.value='123';expect(form.dispatchEvent(new Event('submit',{cancelable:true,bubbles:true}))).toBe(false);
 }finally{document.documentElement.classList.toggle('dark',wasDark);}
});
