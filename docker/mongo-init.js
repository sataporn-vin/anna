const database = db.getSiblingDB("personal_memory");

database.createUser({
  user: process.env.MONGO_APP_USERNAME,
  pwd: process.env.MONGO_APP_PASSWORD,
  roles: [{ role: "readWrite", db: "personal_memory" }],
});
