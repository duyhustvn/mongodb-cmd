use actix_web::{HttpResponse, Responder, get, web};
use futures::TryStreamExt;
use mongodb::bson::doc;
use mongodb::{Collection, bson::Document, options::ClientOptions, options::FindOptions};
use serde::Deserialize;

use crate::config::MongodbConfig;

#[derive(Deserialize, Debug)]
#[serde(rename_all = "lowercase")]
enum OrderType {
    Asc,
    Desc,
}

#[derive(Deserialize)]
struct GetDatabaseProfileQueryString {
    endpoint: String,
    database: String,
    limit: Option<i64>,
    collection: Option<String>,
    duration: Option<i64>,
    offset: Option<u64>,
    order_by: Option<String>,
    order_type: Option<OrderType>,
    operation: Option<String>,
    unique: Option<bool>,
}

#[get("/mongodb-cmd/profile")]
async fn get_profile_detail(query: web::Query<GetDatabaseProfileQueryString>) -> impl Responder {
    let mongodb_config = MongodbConfig::new();
    let mut url = String::new();

    for mongodb_url in mongodb_config.url {
        // println!("mongodb_url: {}", mongodb_url);
        // println!("endpoint: {}", &query.endpoint);
        if mongodb_url.contains(&query.endpoint) {
            url = mongodb_url.clone();
            break;
        }
    }

    if url.is_empty() {
        return HttpResponse::BadRequest().body("Invalid url");
    }
    // println!("url: {}", url);

    let options = ClientOptions::parse(&url)
        .await
        .expect("Failed to parse Mongodb url");

    let client = mongodb::Client::with_options(options).expect("Failed to init MongoDB client");
    let db = client.database(&query.database);
    let col: Collection<Document> = db.collection("system.profile");

    let limit = query.limit.unwrap_or(20); // Default to 20 if not provided
    let offset = query.offset.unwrap_or(0); // Default to 0 if not provided
    let order_by = query.order_by.as_deref().unwrap_or("ts");
    let order_type = match query.order_type {
        Some(OrderType::Asc) => 1,
        Some(OrderType::Desc) => -1,
        None => -1, // default to desc
    };

    let find_options = FindOptions::builder()
        .sort(doc! {order_by: order_type})
        .limit(limit)
        .skip(offset)
        .build();

    let mut filter = doc! {};

    if let Some(duration_ms) = query.duration {
        filter.insert("millis", doc! {"$gt": duration_ms});
    }

    if let Some(collection_name) = &query.collection {
        let namespace = format!("{}.{}", query.database, collection_name);
        filter.insert("ns", namespace);
    }

    if let Some(operation) = &query.operation {
        filter.insert("op", operation);
    }

    let unique = query.unique.unwrap_or(false);
    if unique {
        let pipeline = vec![
            // filter
            doc! { "$match": filter },
            // group
            doc! {
                "$group": {
                    "_id": "$queryHash",
                    "count": {"$sum": 1},
                    "max_millis": {"$max": "$millis"},
                    "avg_millis": {"$avg": "$millis"},
                    "latest_ts": {"$max": "$ts"},
                },
            },
            // sort
            doc! {"$sort": {"count": -1}},
            // pagination
            doc! {"$skip": offset as i64},
            doc! {"$limit": limit},
        ];

        let cursor = col.aggregate(pipeline).await;

        match cursor {
            Ok(cursor) => {
                let docs: Vec<Document> = match cursor.try_collect().await {
                    Ok(d) => d,
                    Err(err) => return HttpResponse::InternalServerError().body(err.to_string()),
                };
                return HttpResponse::Ok().json(docs);
            }
            Err(err) => return HttpResponse::InternalServerError().body(err.to_string()),
        }
    } else {
        let mut cursor = match col.find(filter).with_options(find_options).await {
            Ok(cursor) => cursor,
            Err(err) => return HttpResponse::InternalServerError().body(err.to_string()),
        };

        let mut documents = Vec::new();
        while let Ok(Some(doc)) = cursor.try_next().await {
            documents.push(doc);
        }

        HttpResponse::Ok().json(documents)
    }
}
