import base64
import os

import ldb
from samba.auth import system_session
from samba.param import LoadParm
from samba.samdb import SamDB

lp = LoadParm()
lp.load_default()
db = SamDB(url="/var/lib/samba/private/sam.ldb", session_info=system_session(), lp=lp)
base = str(db.domain_dn())
photos = os.path.join(os.path.dirname(__file__), "photos")


def dn(rdn):
    return f"{rdn},{base}"


for ou in ("OU=Umbrella", "OU=Users,OU=Umbrella", "OU=Groups,OU=Umbrella", "OU=Service,OU=Umbrella"):
    db.create_ou(dn(ou))

db.newuser("svc-umbrella", os.environ["SERVICE_PASSWORD"], userou="OU=Service,OU=Umbrella",
           description="Umbrella Monitoring directory service account")

people = [
    dict(username="ivanov", surname="Иванов", givenname="Пётр", middle="Сергеевич", jobtitle="Руководитель мониторинга",
         department="Мониторинг", mail="ivanov@corp.umbrella.lab", manager=None),
    dict(username="smirnova", surname="Смирнова", givenname="Ольга", middle="Викторовна", jobtitle="Инженер мониторинга",
         department="Мониторинг", mail="smirnova@corp.umbrella.lab", manager="ivanov"),
    dict(username="petrov", surname="Петров", givenname="Алексей", middle=None, jobtitle="Аналитик",
         department="Аналитика", mail="petrov@corp.umbrella.lab", manager="ivanov"),
]

user_dn = {}
for p in people:
    db.newuser(p["username"], os.environ["USER_PASSWORD"], userou="OU=Users,OU=Umbrella", surname=p["surname"],
               givenname=p["givenname"], jobtitle=p["jobtitle"], department=p["department"], mailaddress=p["mail"])
    res = db.search(base=base, scope=ldb.SCOPE_SUBTREE, expression=f"(sAMAccountName={p['username']})", attrs=["dn"])
    user_dn[p["username"]] = str(res[0].dn)

for name in ["svc-umbrella"] + [p["username"] for p in people]:
    db.setexpiry(f"(sAMAccountName={name})", 0, no_expiry_req=True)

for p in people:
    m = ldb.Message(ldb.Dn(db, user_dn[p["username"]]))
    displayname = " ".join(x for x in (p["surname"], p["givenname"], p["middle"]) if x)
    m["displayName"] = ldb.MessageElement(displayname, ldb.FLAG_MOD_REPLACE, "displayName")
    if p["middle"]:
        m["middleName"] = ldb.MessageElement(p["middle"], ldb.FLAG_MOD_REPLACE, "middleName")
    if p["manager"]:
        m["manager"] = ldb.MessageElement(user_dn[p["manager"]], ldb.FLAG_MOD_REPLACE, "manager")
    with open(os.path.join(photos, p["username"] + ".jpg"), "rb") as f:
        m["thumbnailPhoto"] = ldb.MessageElement(f.read(), ldb.FLAG_MOD_REPLACE, "thumbnailPhoto")
    db.modify(m)

db.newgroup("Umbrella Admins", groupou="OU=Groups,OU=Umbrella", description="Umbrella Monitoring administrators")
db.newgroup("Monitoring Team", groupou="OU=Groups,OU=Umbrella", description="Monitoring engineers")
db.newgroup("Umbrella Users", groupou="OU=Groups,OU=Umbrella", description="Umbrella Monitoring users")
db.add_remove_group_members("Monitoring Team", ["smirnova"], add_members_operation=True)
db.add_remove_group_members("Umbrella Admins", ["ivanov", "Monitoring Team"], add_members_operation=True)
db.add_remove_group_members("Umbrella Users", ["ivanov", "smirnova", "petrov"], add_members_operation=True)

print("samba-dc: seeded OU=Umbrella with svc-umbrella, 3 users and 3 groups")
