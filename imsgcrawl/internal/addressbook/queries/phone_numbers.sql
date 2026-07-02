select
  coalesce(p.ZFULLNUMBER, ''),
  coalesce(p.ZCOUNTRYCODE, ''),
  coalesce(p.ZAREACODE, ''),
  coalesce(p.ZLOCALNUMBER, ''),
  coalesce(p.ZOWNER, 0),
  coalesce(r.ZFIRSTNAME, ''),
  coalesce(r.ZLASTNAME, ''),
  coalesce(r.ZORGANIZATION, '')
from ZABCDPHONENUMBER p
join ZABCDRECORD r on r.Z_PK = p.ZOWNER
where nullif(trim(coalesce(nullif(p.ZFULLNUMBER, ''), p.ZLOCALNUMBER, '')), '') is not null
order by p.Z_PK
