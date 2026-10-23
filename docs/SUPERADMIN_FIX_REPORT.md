# Super-admin kod tuzatishlari — 2026-10-23

Holat: kod o‘zgartirildi; test, build, lint, migratsiya, deploy va production sinovi bajarilmadi (buyurtmachi talabiga ko‘ra). Ushbu hujjat testdan o‘tganlik yoki platformaning production uchun tayyorligi tasdig‘i emas.

## O‘zgargan xatti-harakat

- `/api/admin/super/register` endi amaldagi SUPER_ADMIN access tokeni va central DB'dagi faol sessiyani talab qiladi. Birinchi akkaunt uchun HTTP'dan tashqari `cmd/bootstrap-superadmin` qo‘shildi; jadvalda hatto o‘chirilgan akkaunt bo‘lsa ham bootstrap rad etiladi.
- `auth_version` bilan faol super-adminni har bir admin so‘rovida tekshirish qo‘shildi. Logout va parol almashishi versiyani oshirib, barcha qurilmalardagi eski access/refresh tokenlarni bekor qiladi. Eski versiyasiz tokenlar qayta login talab qiladi.
- Super-admin access tokeni 30 daqiqa, refresh tokeni 7 kun. Refresh bitta ishlatishlik token emas; alohida token oilasi/replay detection joriy qilinmagan.
- Admin API tokenni URL query orqali qabul qilmaydi; Authorization header majburiy. JWT tekshirish HS256 bilan chegaralandi. Admin javoblari va super-admin refresh uchun no-store.
- Login: tarmoq peer'i bo‘yicha 10 so‘rov/minut; tanasi 16 KiB bilan chegaralangan. Mavjud bo‘lmagan telefon va noto‘g‘ri parol bir xil 401 javob beradi.
- Default JWT secret olib tashlandi. Kamida 32 baytli aniq belgilangan JWT_SECRET bo‘lmasa server ishga tushmaydi.
- Maktab JSON javoblaridan DB connection string olib tashlandi. School handler'dagi raw DB xato tafsilotlari mijozga yuborilmaydi.
- Maktab nomi, school UUID, admin ismi/telefoni va paroli validatsiyasi qo‘shildi. Admin paroli kamida 12 Unicode belgi, ko‘pi bilan 72 bayt. Duplicate telefon/email 409 qaytaradi. Yangi school admin javobida yaratilgan sana qaytariladi.
- Maktab provisioning'i central transaction advisory lock bilan ketma-ket bajariladi. Bir xil nom yoki sanitizatsiyadan keyin mavjud DB nomiga tushish eski bazani yangi maktabga ulab yubormaydi; so‘rov rad etiladi.
- Mavjud tenant migratsiyasi maktab nomidan qayta hisoblanmaydi: ro‘yxatdagi connection string ishlatiladi. Dirty migratsiya avtomatik Force qilinmaydi. Migratsiya xatosida server trafik/botlarni boshlamaydi.

## Ishga tushirishga ta’siri

1. Backend va `farzandim-frontend`dagi bir xil nomli branch o‘zgarishlarini birga ko‘rib chiqing. `main`ga merge yoki deploy qilinmagan.
2. Central migratsiya `000005_super_admin_auth_version` kerak; odatiy backend startup migratsiya mexanizmi uni qo‘llaydi. Sinovdan oldin backup oling. Migratsiyani bu ish davomida bajarmadik.
3. JWT_SECRET kamida 32 baytli tasodifiy secret bo‘lsin. Yetarli mavjud secretni saqlash mumkin; uni almashtirish tenant tokenlariga ham ta’sir qiladi.
4. Barcha super-adminlar qayta login qiladi. Frontend production build vaqtida NEXT_PUBLIC_API_URL to‘liq HTTPS origin bo‘lishi shart, masalan `https://api.example.uz` (path/query/credential qo‘shmang). Lokal fallback `http://localhost:6560`.
5. Yangi bo‘sh tizimda central migratsiyalardan keyin, repo ildizidan `go run ./cmd/bootstrap-superadmin` operator tomonidan ishlatiladi. CENTRAL_DB_URL hamda BOOTSTRAP_ADMIN_PHONE, BOOTSTRAP_ADMIN_FIRST_NAME, BOOTSTRAP_ADMIN_LAST_NAME, BOOTSTRAP_ADMIN_PASSWORD muhit o‘zgaruvchilarini xavfsiz kiriting. Parolni git, shell history yoki loglarga yozmang; tugagach bootstrap qiymatlarini olib tashlang. Bu account recovery vositasi emas.
6. Provisioning DB yaratish va central metadata commit'ini bitta PostgreSQL transactionga birlashtira olmaydi. O‘rtada xato bo‘lsa ro‘yxatdan o‘tmagan DB qolishi mumkin. Avtomatik drop/adopt yo‘q; operator tekshiradi. Nom kolliziyasi bo‘lsa boshqa nom tanlang; mavjud ma’lumotni o‘chirmang.
7. Login limit process-local va RemoteAddr asosida. Reverse proxy ortida barcha foydalanuvchilar bir bucketga tushishi mumkin. Production proxy'da haqiqiy client IP bo‘yicha limitni alohida sozlang; ko‘p instansli umumiy limiter bu patchda yo‘q.

## Foydalanuvchiga qoldirilgan tekshiruvlar

- Backend compile/test; frontend typecheck/lint/build. Biz ishga tushirmadik.
- Tokensiz va tenant tokeni bilan super-admin registratsiya/admin API rad etilsin; URL query token ham rad etilsin.
- To‘g‘ri login, noto‘g‘ri login, 11-so‘rovda 429 va keyingi minutda tiklanish.
- Access muddati tugaganda parallel API so‘rovlar bitta refresh bilan davom etsin. Tarmoq/503 xatosi foydalanuvchiga ko‘rinsin.
- Logout/parol almashgach boshqa qurilmadagi eski access va refresh ishlamasin; yangi parolda login ishlasin.
- School create/list/detail javoblarida db_connection_string bo‘lmasin.
- Takroriy nom, `Alpha School`/`Alpha-School` kabi DB nomi kolliziyasi, uzun/bo‘sh nom rad etilsin; mavjud DB ma’lumotlari o‘zgarmasin.
- School admin qo‘shish, duplicate telefon/email, qisqa parol, sana va modal ichidagi xato ko‘rsatilishi.
- Maktablar yuklanayotganda va server xatosida bo‘sh ro‘yxat deb noto‘g‘ri ko‘rsatilmasin. Clipboard ruxsati yo‘qligida xato ko‘rinsin.
- Faqat alohida sinov bazasida migratsiya xatosi/dirty holat va bootstrap'ni tekshiring; ikkinchi bootstrap rad etilsin.

## Chegaralar

Bu super-admin oqimiga qaratilgan patch. Parent/teacher APK, school-frontend va oldingi auditdagi boshqa tenant authorization muammolari bu ish doirasida tuzatilmadi. Frontend tokenlari hali localStorage'da; HttpOnly cookie/BFF va MFA qo‘shilmadi. XSSga qarshi qatlamlar va to‘liq actor attribution bilan super-admin audit trail alohida ish. Mavjud kuchsiz account parollari avtomatik almashtirilmaydi. Shu sababli barcha platforma xavflari yopildi yoki production tayyor deb hisoblamang.
