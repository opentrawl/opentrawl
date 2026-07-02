select
  coalesce(e.ZADDRESS, ''),
  coalesce(e.ZOWNER, 0),
  coalesce(r.ZFIRSTNAME, ''),
  coalesce(r.ZLASTNAME, ''),
  coalesce(r.ZORGANIZATION, ''),
  {{IS_ME_EXPR}}
from ZABCDEMAILADDRESS e
join ZABCDRECORD r on r.Z_PK = e.ZOWNER
where nullif(trim(e.ZADDRESS), '') is not null
order by e.Z_PK
